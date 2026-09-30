// prune.go - context pruning (stage 15 of docs/ROADMAP.md).
//
// The context window is the small model's scarcest resource, and raw
// tool outputs are what floods it: one long directory listing costs
// more than a day of chat. Pruning replaces the old hard truncation of
// the history (last 20 messages, whatever they were) with an ordered,
// budget-driven pass over the conversation the agent is about to send:
//
//  1. If the history fits the budget, nothing is touched.
//  2. Old tool outputs (outside the protected tail) are truncated to a
//     keep-prefix plus a marker. Cheap, lossless for recent work.
//  3. If it still does not fit, the middle turns - everything between
//     the protected head and the protected tail - are compacted into
//     one message: a summary from an auxiliary model pass when a
//     Summarize function is configured, or an explicit omission marker
//     when it is not (or when it fails). The marker says how many
//     turns were dropped, so the model is never silently gaslit about
//     its own past.
//
// Invariants, enforced by construction and tested exhaustively:
//
//   - The head (first exchange) and the recent tail always survive
//     byte-identical: that is where the facts and the live thread are.
//   - A tool call is never split from its result: messages are grouped
//     into blocks (an assistant message with tool calls plus the tool
//     messages answering it), and cuts only ever land between blocks.
//   - The store keeps the full conversation: pruning rewrites only the
//     in-memory copy sent to the model, never what is persisted.
package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/FiveTechSoft/FiveAgent/internal/model"
)

// PruneConfig tunes the pruner. Zero values take the defaults noted on
// each field. MaxChars budgets the history characters sent to the model
// (the system prompt and memory note ride outside this budget).
type PruneConfig struct {
	// MaxChars is the history budget. Default 24000 (~6-8k tokens of
	// Spanish), sized for small local contexts.
	MaxChars int
	// HeadKeep is how many leading messages are always protected.
	// Default 2: the first exchange, where the facts usually are.
	HeadKeep int
	// TailKeep is how many trailing messages are always protected.
	// Default 8: the live thread.
	TailKeep int
	// ToolOutputKeep is how many leading chars survive when an old
	// tool output is truncated. Default 400.
	ToolOutputKeep int
	// Summarize compacts the middle turns into a short brief. When nil
	// (or on error) the middle is replaced by an omission marker.
	Summarize func(ctx context.Context, turns []model.Message) (string, error)
	// DigestSink, when set, receives the summary each time step 2
	// compacts middle turns successfully: the rolling session digest
	// (stage 7g) persists it to memory, so detail dropped from the
	// live history survives as recallable data. Never called on the
	// marker fallback - nothing is summarized then, and writing down
	// "N turns omitted" would be noise, not memory.
	DigestSink func(summary string, turns int)
}

func (cfg PruneConfig) withDefaults() PruneConfig {
	if cfg.MaxChars <= 0 {
		cfg.MaxChars = 24000
	}
	if cfg.HeadKeep <= 0 {
		cfg.HeadKeep = 2
	}
	if cfg.TailKeep <= 0 {
		cfg.TailKeep = 8
	}
	if cfg.ToolOutputKeep <= 0 {
		cfg.ToolOutputKeep = 400
	}
	return cfg
}

const truncatedByPruning = " …[truncated by context pruning]"

// block is a range of messages that must stay together: an assistant
// message carrying tool calls plus the tool messages answering it, or
// any single message on its own.
type block struct{ start, end int }

func messageBlocks(msgs []model.Message) []block {
	var bs []block
	for i := 0; i < len(msgs); {
		j := i + 1
		if msgs[i].Role == "assistant" && len(msgs[i].ToolCalls) > 0 {
			for j < len(msgs) && msgs[j].Role == "tool" {
				j++
			}
		}
		bs = append(bs, block{i, j})
		i = j
	}
	return bs
}

func totalChars(msgs []model.Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content)
	}
	return n
}

// cutRunes truncates s to at most n bytes without splitting a UTF-8
// rune.
func cutRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && (s[n]&0xC0) == 0x80 {
		n--
	}
	return s[:n]
}

// Prune applies the ordered pruning to msgs and returns the pruned
// copy plus a human-readable note per action taken (the agent logs
// them like any other turn event). The input slice is never mutated.
func (cfg PruneConfig) Prune(ctx context.Context, msgs []model.Message) ([]model.Message, []string) {
	cfg = cfg.withDefaults()
	if totalChars(msgs) <= cfg.MaxChars {
		return msgs, nil
	}
	out := append([]model.Message(nil), msgs...)
	var notes []string

	// Step 1: truncate old tool outputs, sparing the protected tail.
	tailStart := len(out) - cfg.TailKeep
	if tailStart < 0 {
		tailStart = 0
	}
	truncated, freed := 0, 0
	for i := 0; i < tailStart; i++ {
		if out[i].Role == "tool" && len(out[i].Content) > cfg.ToolOutputKeep {
			freed += len(out[i].Content) - cfg.ToolOutputKeep
			out[i].Content = cutRunes(out[i].Content, cfg.ToolOutputKeep) + truncatedByPruning
			truncated++
		}
	}
	if truncated > 0 {
		notes = append(notes, fmt.Sprintf("truncated %d old tool output(s), freed ~%d chars", truncated, freed))
	}
	if totalChars(out) <= cfg.MaxChars {
		return out, notes
	}

	// Step 2: compact the middle. Head and tail are counted in whole
	// blocks, so a cut can never land inside a tool call/result pair.
	bs := messageBlocks(out)
	headMsgs, headEnd := 0, 0 // block index past the head
	for headEnd < len(bs) && headMsgs < cfg.HeadKeep {
		headMsgs += bs[headEnd].end - bs[headEnd].start
		headEnd++
	}
	tailMsgs, tailBeg := 0, len(bs) // block index where the tail starts
	for tailBeg > headEnd && tailMsgs < cfg.TailKeep {
		tailBeg--
		tailMsgs += bs[tailBeg].end - bs[tailBeg].start
	}
	if headEnd >= tailBeg {
		return out, append(notes, "over budget but head and tail leave no prunable middle")
	}
	mid0, mid1 := bs[headEnd].start, bs[tailBeg].start
	middle := out[mid0:mid1]
	midChars := totalChars(middle)

	var replacement string
	if cfg.Summarize != nil {
		if sum, err := cfg.Summarize(ctx, middle); err == nil && strings.TrimSpace(sum) != "" {
			replacement = fmt.Sprintf("[Summary of %d earlier turns, made to fit the context budget: %s]",
				len(middle), strings.TrimSpace(sum))
			notes = append(notes, fmt.Sprintf("summarized %d middle turns (%d chars) with the auxiliary pass", len(middle), midChars))
			if cfg.DigestSink != nil {
				cfg.DigestSink(strings.TrimSpace(sum), len(middle))
			}
		} else {
			notes = append(notes, "summarizer unavailable or failed; middle turns omitted instead")
		}
	}
	if replacement == "" {
		replacement = fmt.Sprintf("[%d earlier turns omitted to fit the context budget]", len(middle))
		notes = append(notes, fmt.Sprintf("omitted %d middle turns (%d chars)", len(middle), midChars))
	}
	pruned := make([]model.Message, 0, len(out)-len(middle)+1)
	pruned = append(pruned, out[:mid0]...)
	pruned = append(pruned, model.Message{Role: "system", Content: replacement})
	pruned = append(pruned, out[mid1:]...)
	return pruned, notes
}
