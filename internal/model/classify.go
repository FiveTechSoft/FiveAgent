// classify.go - typed model-API failures (stage 16 of docs/ROADMAP.md).
//
// The agent's recovery pipeline needs to know WHAT failed, not parse
// error strings downstream. The client classifies every failure at the
// source - where the HTTP status, the body and the net error are still
// available - into a Failure with a Kind. The recovery table in
// internal/agent maps each Kind to its recovery: retry, fallback to
// the other configured model, compress the context, or abort with an
// honest message.
package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

// FailureKind is the classified shape of one model-API failure.
type FailureKind int

const (
	FailureUnknown FailureKind = iota
	// FailureTimeout: the call exceeded the configured model timeout.
	FailureTimeout
	// FailureRateLimit: HTTP 429 - back off and retry.
	FailureRateLimit
	// FailureAuth: HTTP 401/403 - the credential was rejected.
	FailureAuth
	// FailureOverflow: the endpoint rejected the request as too large
	// for its context (HTTP 400/413/500 with a context-size marker).
	FailureOverflow
	// FailureMalformed: the reply was not a valid chat response (bad
	// JSON, no choices) - the endpoint or a proxy mangled it.
	FailureMalformed
	// FailureUnavailable: the endpoint cannot be reached or is down
	// (connection errors, 502/503/504).
	FailureUnavailable
	// FailureEmpty is not a transport or API error: the endpoint
	// answered 200 with empty content and no tool calls. The agent
	// layer synthesizes it so the same recovery ladder handles it.
	FailureEmpty
)

func (k FailureKind) String() string {
	switch k {
	case FailureTimeout:
		return "timeout"
	case FailureRateLimit:
		return "rate-limit"
	case FailureAuth:
		return "auth"
	case FailureOverflow:
		return "context-overflow"
	case FailureMalformed:
		return "malformed-reply"
	case FailureUnavailable:
		return "unavailable"
	case FailureEmpty:
		return "empty-reply"
	}
	return "unknown"
}

// Failure is one classified model-API failure. Status carries the HTTP
// status when there was a response at all.
type Failure struct {
	Kind   FailureKind
	Status int
	Err    error
}

func (f *Failure) Error() string {
	if f.Status != 0 {
		return fmt.Sprintf("model: %s (HTTP %d): %v", f.Kind, f.Status, f.Err)
	}
	return fmt.Sprintf("model: %s: %v", f.Kind, f.Err)
}

func (f *Failure) Unwrap() error { return f.Err }

// contextMarkers are the body fragments endpoints use to say the
// request was too big for the model's context. llama.cpp, Ollama and
// the usual proxies each phrase it their own way.
var contextMarkers = []string{
	"context size", "context length", "context window",
	"too many tokens", "maximum context", "exceeds the context",
	"context_length_exceeded", "reduce the length",
}

// classifyStatus maps an HTTP error status (with its body) to a Kind.
func classifyStatus(status int, body []byte) FailureKind {
	text := strings.ToLower(string(body))
	switch status {
	case 429:
		return FailureRateLimit
	case 401, 403:
		return FailureAuth
	case 502, 503, 504:
		return FailureUnavailable
	case 400, 413, 500:
		for _, m := range contextMarkers {
			if strings.Contains(text, m) {
				return FailureOverflow
			}
		}
		if status == 500 {
			return FailureUnavailable
		}
	}
	return FailureUnknown
}

// classifyNetError maps a transport-level error (no response at all)
// to a Kind.
func classifyNetError(err error) FailureKind {
	if errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err) {
		return FailureTimeout
	}
	return FailureUnavailable
}
