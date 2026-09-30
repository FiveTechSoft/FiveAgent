package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/model"
)

// pairsOK asserts the never-split invariant over a pruned
// conversation: every assistant message carrying tool calls is
// followed by exactly its tool messages, and every tool message is
// preceded by its call.
func pairsOK(t *testing.T, msgs []model.Message) {
	t.Helper()
	for i, m := range msgs {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			want := len(m.ToolCalls)
			for j := 0; j < want; j++ {
				if i+1+j >= len(msgs) || msgs[i+1+j].Role != "tool" {
					t.Fatalf("split pair: assistant call at %d lacks tool result %d", i, j)
				}
			}
		}
		if m.Role == "tool" {
			if i == 0 || !(msgs[i-1].Role == "tool" || (msgs[i-1].Role == "assistant" && len(msgs[i-1].ToolCalls) > 0)) {
				t.Fatalf("orphan tool message at %d", i)
			}
		}
	}
}

// scriptedConversation builds n user/assistant turns; every 6th turn
// is a tool turn with a verbose output of toolChars bytes. The first
// user message carries the given fact.
func scriptedConversation(n int, fact string, toolChars int) []model.Message {
	msgs := []model.Message{{Role: "user", Content: "hola, recuerda esto: " + fact}}
	msgs = append(msgs, model.Message{Role: "assistant", Content: "apuntado: " + fact})
	for turn := 2; turn <= n; turn++ {
		msgs = append(msgs, model.Message{Role: "user", Content: fmt.Sprintf("turno %d: cuéntame algo", turn)})
		if turn%6 == 0 {
			msgs = append(msgs,
				model.Message{Role: "assistant", ToolCalls: []model.ToolCall{{ID: "c1"}}},
				model.Message{Role: "tool", ToolCallID: "c1", Content: strings.Repeat("listado ", toolChars/8)},
				model.Message{Role: "assistant", Content: fmt.Sprintf("turno %d: ahí va el listado", turn)},
			)
		} else {
			msgs = append(msgs, model.Message{Role: "assistant", Content: fmt.Sprintf("turno %d: respuesta", turn)})
		}
	}
	return msgs
}

// (1) Under budget the conversation passes through untouched.
func TestPruneUnderBudgetUntouched(t *testing.T) {
	msgs := scriptedConversation(10, "mi perro se llama Toby", 800)
	out, notes := PruneConfig{MaxChars: 1 << 20}.Prune(context.Background(), msgs)
	if len(out) != len(msgs) || notes != nil {
		t.Fatalf("under budget must not touch anything: %d -> %d, notes %v", len(msgs), len(out), notes)
	}
}

// (2) Over budget: old tool outputs are truncated first, tail output
// survives, and if that suffices no turn is dropped.
func TestPruneTruncatesOldToolOutputsFirst(t *testing.T) {
	msgs := scriptedConversation(30, "mi perro se llama Toby", 4000)
	budget := totalChars(msgs) - 5000 // just over: truncation alone must suffice
	out, notes := PruneConfig{MaxChars: budget, ToolOutputKeep: 300}.Prune(context.Background(), msgs)
	if totalChars(out) > budget {
		t.Fatalf("still over budget: %d > %d", totalChars(out), budget)
	}
	if len(notes) == 0 || !strings.Contains(notes[0], "truncated") {
		t.Fatalf("expected truncation note, got %v", notes)
	}
	if strings.Contains(strings.Join(notes, " "), "omitted") {
		t.Fatalf("truncation should have sufficed: %v", notes)
	}
	// Old outputs carry the marker; the tail one (inside TailKeep) does not.
	foundMarker := false
	for _, m := range out[:len(out)-8] {
		if m.Role == "tool" && strings.Contains(m.Content, truncatedByPruning) {
			foundMarker = true
			if len(m.Content) > 300+len(truncatedByPruning) {
				t.Fatalf("truncated output too long: %d", len(m.Content))
			}
		}
	}
	if !foundMarker {
		t.Fatal("no truncated old tool output found")
	}
	for _, m := range out[len(out)-8:] {
		if m.Role == "tool" && strings.Contains(m.Content, truncatedByPruning) {
			t.Fatal("tail tool output must survive intact")
		}
	}
	pairsOK(t, out)
}

// (3) Way over budget: middle turns are summarized, head and tail
// survive byte-identical.
func TestPruneSummarizesMiddleProtectingHeadAndTail(t *testing.T) {
	fact := "mi perro se llama Toby"
	msgs := scriptedConversation(60, fact, 4000)
	var gotTurns []model.Message
	cfg := PruneConfig{
		MaxChars:       8000,
		ToolOutputKeep: 200,
		Summarize: func(_ context.Context, turns []model.Message) (string, error) {
			gotTurns = turns
			return "resumen de prueba: turnos intermedios sobre listados", nil
		},
	}
	out, notes := cfg.Prune(context.Background(), msgs)
	if totalChars(out) > 8000+512 { // budget plus one marker's slack
		t.Fatalf("over budget: %d", totalChars(out))
	}
	if gotTurns == nil {
		t.Fatal("summarizer was not called")
	}
	if !strings.Contains(out[0].Content, fact) || !strings.Contains(out[1].Content, fact) {
		t.Fatal("head exchange (the fact) must survive byte-identical")
	}
	for i, m := range msgs[len(msgs)-8:] {
		if out[len(out)-8+i].Content != m.Content {
			t.Fatal("tail must survive byte-identical")
		}
	}
	joined := ""
	for _, n := range notes {
		joined += n
	}
	if !strings.Contains(joined, "summarized") {
		t.Fatalf("expected summary note, got %v", notes)
	}
	pairsOK(t, out)
}

// (4) Without a summarizer the middle is replaced by an explicit
// omission marker that says how many turns were dropped.
func TestPruneOmissionMarkerWithoutSummarizer(t *testing.T) {
	msgs := scriptedConversation(60, "mi perro se llama Toby", 4000)
	out, notes := PruneConfig{MaxChars: 8000, ToolOutputKeep: 200}.Prune(context.Background(), msgs)
	found := false
	for _, m := range out {
		if strings.Contains(m.Content, "earlier turns omitted") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no omission marker: %v", notes)
	}
	pairsOK(t, out)
}

// (5) A summarizer failure falls back to the omission marker, never
// to a panic or an empty hole.
func TestPruneSummarizerFailureFallsBack(t *testing.T) {
	msgs := scriptedConversation(60, "mi perro se llama Toby", 4000)
	cfg := PruneConfig{
		MaxChars:       8000,
		ToolOutputKeep: 200,
		Summarize:      func(context.Context, []model.Message) (string, error) { return "", fmt.Errorf("model down") },
	}
	out, _ := cfg.Prune(context.Background(), msgs)
	for _, m := range out {
		if strings.Contains(m.Content, "earlier turns omitted") {
			return
		}
	}
	t.Fatal("summarizer failure must fall back to the omission marker")
}

// (6) A cut can never split a tool call from its result, even when
// the protected-tail boundary would naturally land between them.
func TestPruneNeverSplitsToolPair(t *testing.T) {
	// Two big tool blocks right at the tail boundary.
	msgs := []model.Message{{Role: "user", Content: "inicio: mi perro se llama Toby"}}
	msgs = append(msgs, model.Message{Role: "assistant", Content: "apuntado"})
	for i := 0; i < 20; i++ {
		msgs = append(msgs,
			model.Message{Role: "user", Content: strings.Repeat("x", 900)},
			model.Message{Role: "assistant", ToolCalls: []model.ToolCall{{ID: "a"}, {ID: "b"}}},
			model.Message{Role: "tool", ToolCallID: "a", Content: strings.Repeat("y", 900)},
			model.Message{Role: "tool", ToolCallID: "b", Content: strings.Repeat("z", 900)},
			model.Message{Role: "assistant", Content: "listo"},
		)
	}
	for _, tail := range []int{1, 2, 3, 5, 8, 13} {
		out, _ := PruneConfig{MaxChars: 4000, ToolOutputKeep: 100, TailKeep: tail}.Prune(context.Background(), msgs)
		pairsOK(t, out)
	}
}

// (7) Head and tail alone over budget: the pruner reports it cannot
// help instead of eating the protected regions.
func TestPruneReportsUnprunableConversation(t *testing.T) {
	msgs := []model.Message{
		{Role: "user", Content: strings.Repeat("a", 3000)},
		{Role: "assistant", Content: strings.Repeat("b", 3000)},
		{Role: "user", Content: strings.Repeat("c", 3000)},
		{Role: "assistant", Content: strings.Repeat("d", 3000)},
	}
	out, notes := PruneConfig{MaxChars: 2000, HeadKeep: 2, TailKeep: 2}.Prune(context.Background(), msgs)
	if len(out) != len(msgs) {
		t.Fatal("protected regions must survive even over budget")
	}
	joined := strings.Join(notes, " ")
	if !strings.Contains(joined, "no prunable middle") {
		t.Fatalf("expected the honest note, got %v", notes)
	}
}

// (8) Stage 7g: a successful compaction reports its summary to the
// DigestSink exactly once; the marker fallback paths (no summarizer,
// failing summarizer, under budget) never call it - writing "N turns
// omitted" down would be noise, not memory.
func TestPruneDigestSink(t *testing.T) {
	var gotSummary string
	var gotTurns, calls int
	cfg := PruneConfig{
		MaxChars:       8000,
		ToolOutputKeep: 200,
		Summarize: func(context.Context, []model.Message) (string, error) {
			return "resumen de prueba", nil
		},
		DigestSink: func(summary string, turns int) {
			calls++
			gotSummary, gotTurns = summary, turns
		},
	}
	cfg.Prune(context.Background(), scriptedConversation(60, "mi perro se llama Toby", 4000))
	if calls != 1 {
		t.Fatalf("digest sink calls = %d, want 1", calls)
	}
	if gotSummary != "resumen de prueba" || gotTurns == 0 {
		t.Fatalf("digest sink got (%q, %d turns)", gotSummary, gotTurns)
	}

	// No summarizer: compaction happens via the marker, sink silent.
	calls = 0
	cfg.Summarize = nil
	_, notes := cfg.Prune(context.Background(), scriptedConversation(60, "mi perro se llama Toby", 4000))
	if calls != 0 {
		t.Fatal("digest sink fired on the marker fallback")
	}
	joined := ""
	for _, n := range notes {
		joined += n
	}
	if !strings.Contains(joined, "omitted") {
		t.Fatalf("expected the omission path, got %v", notes)
	}

	// Under budget: no pruning at all, sink silent.
	calls = 0
	PruneConfig{MaxChars: 1 << 20, DigestSink: cfg.DigestSink}.Prune(
		context.Background(), scriptedConversation(5, "x", 100))
	if calls != 0 {
		t.Fatal("digest sink fired under budget")
	}
}
