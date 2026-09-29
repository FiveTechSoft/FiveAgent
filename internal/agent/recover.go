// recover.go - the error-recovery pipeline (stage 16 of
// docs/ROADMAP.md).
//
// One place maps every classified model-API failure to its recovery,
// instead of string-matching errors inside the loop. The ladder per
// failure kind:
//
//	timeout, malformed reply, rate-limit  -> retry the same model
//	                                        (rate-limit waits longer),
//	                                        then the fallback model
//	empty reply (200, no content, no      -> retry the same model with
//	tool calls)                               a reinforced prompt, then
//	                                        the fallback model; still
//	                                        empty returns the empty
//	                                        answer for the agent's
//	                                        honest-guard path
//	context overflow                      -> compress the context with
//	                                        the stage 15 pruner at half
//	                                        budget, retry once, then
//	                                        the fallback model
//	unavailable                           -> the fallback model directly
//	auth                                  -> honest abort naming the
//	                                        rejected credential (there
//	                                        is no second credential to
//	                                        rotate to in a single-key
//	                                        setup)
//
// When nothing works the turn aborts with one clear error; the
// channels turn it into their standard Spanish fallback line, so the
// user always gets exactly one outcome.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

// Backoffs are short: a personal agent answers a human, not a batch
// job. Rate-limit waits longer by design.
const (
	retryBackoff      = 300 * time.Millisecond
	rateLimitBackoff  = 1500 * time.Millisecond
	maxRecoveryRounds = 2 // retries of the same model before falling back
)

// recoverableChat calls mdl and walks the recovery ladder on a
// classified failure. fallback is the other configured model (nil when
// only one exists).
func (a *Agent) recoverableChat(ctx context.Context, mdl, fallback *model.Client, msgs []model.Message, specs []tools.Spec) (model.Message, error) {
	ans, err := mdl.Chat(ctx, msgs, specs)
	if err == nil && strings.TrimSpace(ans.Content) == "" && len(ans.ToolCalls) == 0 {
		// A 200 with nothing in it is a generation failure, not a
		// success: classify it so the ladder below applies.
		err = &model.Failure{Kind: model.FailureEmpty, Err: fmt.Errorf("model returned empty content and no tool calls")}
	}
	if err == nil {
		return ans, nil
	}
	var f *model.Failure
	if !errors.As(err, &f) {
		return ans, err // not a classified model failure: bubble up
	}
	kind := f.Kind
	log.Printf("agent: model failure classified as %s, starting recovery", kind)

	switch kind {
	case model.FailureAuth:
		// Rotate credential: there is no second credential to rotate
		// to, so the honest outcome is a clear abort.
		return ans, fmt.Errorf("agent: the model endpoint rejected the credential (HTTP %d) - check model.api_key in fiveagent.yml; no alternate credential is configured: %w", f.Status, err)

	case model.FailureTimeout, model.FailureMalformed, model.FailureRateLimit:
		backoff := retryBackoff
		if kind == model.FailureRateLimit {
			backoff = rateLimitBackoff
		}
		for attempt := 1; attempt <= maxRecoveryRounds; attempt++ {
			select {
			case <-ctx.Done():
				return ans, ctx.Err()
			case <-time.After(backoff):
			}
			ans, err = mdl.Chat(ctx, msgs, specs)
			if err == nil {
				log.Printf("agent: recovered from %s on retry %d", kind, attempt)
				return ans, nil
			}
			if !errors.As(err, &f) {
				return ans, err
			}
		}
		log.Printf("agent: %d retries did not fix %s", maxRecoveryRounds, kind)

	case model.FailureOverflow:
		// Compress the context with the stage 15 pruner at half the
		// default budget, then retry once on the compressed request.
		compressed, notes := (PruneConfig{MaxChars: 12000}).Prune(ctx, msgs)
		for _, n := range notes {
			log.Printf("agent: overflow recovery pruning: %s", n)
		}
		if len(compressed) < len(msgs) || totalChars(compressed) < totalChars(msgs) {
			ans, err = mdl.Chat(ctx, compressed, specs)
			if err == nil {
				log.Printf("agent: recovered from context overflow after pruning (%d -> %d chars)", totalChars(msgs), totalChars(compressed))
				return ans, nil
			}
			if !errors.As(err, &f) {
				return ans, err
			}
		}
		log.Printf("agent: context overflow persists after compression")

	case model.FailureEmpty:
		// Retry with a reinforced prompt: the transcript plus an
		// explicit instruction that the last reply came back empty.
		reinforced := append(append([]model.Message{}, msgs...), model.Message{
			Role:    "system",
			Content: "Tu última respuesta llegó vacía. Responde ahora directamente a la petición del usuario, con contenido real.",
		})
		for attempt := 1; attempt <= maxRecoveryRounds; attempt++ {
			ans, err = mdl.Chat(ctx, reinforced, specs)
			if err == nil && (strings.TrimSpace(ans.Content) != "" || len(ans.ToolCalls) > 0) {
				log.Printf("agent: recovered from empty reply on reinforced retry %d", attempt)
				return ans, nil
			}
			if err != nil && !errors.As(err, &f) {
				return ans, err
			}
		}
		log.Printf("agent: %d reinforced retries still returned empty", maxRecoveryRounds)
		// The shared fallback rung below uses the plain transcript;
		// for an empty reply the fallback model also gets the
		// reinforcement, and a still-empty answer is returned as-is
		// (nil error) so the agent's honest-guard path owns the
		// final outcome.
		if fallback != nil && fallback != mdl {
			ans, err = fallback.Chat(ctx, reinforced, specs)
			if err == nil {
				if strings.TrimSpace(ans.Content) != "" || len(ans.ToolCalls) > 0 {
					log.Printf("agent: recovered from empty reply via the fallback model")
				}
				return ans, nil
			}
			var ff *model.Failure
			if errors.As(err, &ff) {
				return ans, fmt.Errorf("agent: both models failed (%s, then %s): %w", kind, ff.Kind, err)
			}
			return ans, err
		}
		return ans, nil

	case model.FailureUnavailable:
		// Straight to the fallback model.
	}

	// Shared last rung: the other configured model.
	if fallback != nil && fallback != mdl {
		ans, err = fallback.Chat(ctx, msgs, specs)
		if err == nil {
			log.Printf("agent: recovered from %s via the fallback model", kind)
			return ans, nil
		}
		var ff *model.Failure
		if errors.As(err, &ff) {
			return ans, fmt.Errorf("agent: both models failed (%s, then %s): %w", kind, ff.Kind, err)
		}
		return ans, err
	}
	return ans, fmt.Errorf("agent: unrecoverable model failure (%s), no fallback model configured: %w", kind, err)
}
