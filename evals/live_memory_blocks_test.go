package evals

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/agent"
	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/memory"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

// TestLiveMemoryBlocksUsed probes the run-11 memory-miss scenarios
// against the real model (FIVEAGENT_EVAL_LIVE=1): an open question
// answered from the frozen snapshot, an open question answered from the
// per-turn recall note (fact saved mid-session, both in a short
// conversation and in a long pruned session like the battery's), and an
// abstention after olvida:. Harness-side availability is asserted first
// in every subtest, so a failure can only be the model not using what
// it got. First live run 2026-10-03: all four pass on qwen3.5:9b with
// the stock prompt - the run-11 misses do not reproduce in isolation,
// so a battery rerun (not a prompt tweak) is the instrument that can
// adjudicate them.
func TestLiveMemoryBlocksUsed(t *testing.T) {
	if os.Getenv("FIVEAGENT_EVAL_LIVE") != "1" {
		t.Skip("live eval: set FIVEAGENT_EVAL_LIVE=1 (needs a running model server)")
	}
	baseURL := os.Getenv("FIVEAGENT_EVAL_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:11434/v1"
	}
	modelName := os.Getenv("FIVEAGENT_EVAL_MODEL")
	if modelName == "" {
		modelName = "qwen3.5:9b"
	}
	ctx := context.Background()
	const lacón = "mi comida favorita es el lacón con grelos"

	newKnowledge := func(t *testing.T) *memory.Knowledge {
		t.Helper()
		kn, err := memory.OpenKnowledge(filepath.Join(t.TempDir(), "memory"))
		if err != nil {
			t.Fatal(err)
		}
		return kn
	}
	newAgent := func(t *testing.T, kn *memory.Knowledge) *agent.Agent {
		t.Helper()
		store, err := memory.OpenJSON(filepath.Join(t.TempDir(), "history.json"))
		if err != nil {
			t.Fatal(err)
		}
		cfg := &config.Config{Model: config.Model{BaseURL: baseURL, Name: modelName}}
		a := agent.New(model.NewOpenAICompat(cfg.Model), store,
			tools.NewRegistry(tools.SaveMemory{K: kn}, tools.ForgetMemory{K: kn}),
			agent.SystemPrompt(cfg))
		a.WithKnowledge(kn)
		return a
	}
	hasFact := func(hits []memory.FileHit, token string) bool {
		for _, h := range hits {
			for _, ln := range h.Lines {
				if strings.Contains(strings.ToLower(ln), strings.ToLower(token)) {
					return true
				}
			}
		}
		return false
	}

	t.Run("snapshot-open-question", func(t *testing.T) {
		kn := newKnowledge(t)
		if _, err := kn.Append("preferences", lacón); err != nil {
			t.Fatal(err)
		}
		if _, err := kn.Append("preferences", "mi plato de fiesta es la empanada"); err != nil {
			t.Fatal(err)
		}
		if _, err := kn.Forget("preferences", "plato de fiesta"); err != nil {
			t.Fatal(err)
		}
		a := newAgent(t, kn)
		reply, err := a.Handle(ctx, "whatsapp", "live", "¿qué sabes de mí?")
		if err != nil {
			t.Fatal(err)
		}
		low := strings.ToLower(reply)
		t.Logf("reply: %s", reply)
		if !strings.Contains(low, "lacón") {
			t.Errorf("snapshot miss: reply never used the injected fact: %q", reply)
		}
		if strings.Contains(low, "empanada") {
			t.Errorf("erased fact leaked from the snapshot: %q", reply)
		}
	})

	t.Run("recall-note-open-question", func(t *testing.T) {
		kn := newKnowledge(t)
		a := newAgent(t, kn)
		if _, err := a.Handle(ctx, "whatsapp", "live", "hola"); err != nil {
			t.Fatal(err)
		}
		if _, err := kn.Append("preferences", lacón); err != nil {
			t.Fatal(err)
		}
		const q = "volviendo a lo de antes del todo: ¿cuál es mi comida favorita ahora mismo?"
		hits, err := kn.Recall(q)
		if err != nil {
			t.Fatal(err)
		}
		if !hasFact(hits, "lacón") {
			t.Fatalf("harness: recall did not surface the fact, probe invalid: %+v", hits)
		}
		reply, err := a.Handle(ctx, "whatsapp", "live", q)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("reply: %s", reply)
		if !strings.Contains(strings.ToLower(reply), "lacón") {
			t.Errorf("recall-note miss: reply never used the recalled fact: %q", reply)
		}
	})

	t.Run("recall-note-long-session", func(t *testing.T) {
		kn := newKnowledge(t)
		store, err := memory.OpenJSON(filepath.Join(t.TempDir(), "history.json"))
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		// Deep pre-seeded history over the pruner's 24000-char budget:
		// the battery's deferred-recall miss happened after ~80 turns
		// with middle turns pruned, not in a two-turn conversation.
		filler := strings.Repeat("charla cotidiana sobre el clima, el trabajo y los planes del fin de semana. ", 20)
		for i := 0; i < 25; i++ {
			if err := store.Append(ctx, "whatsapp", "live", "user", filler); err != nil {
				t.Fatal(err)
			}
			if err := store.Append(ctx, "whatsapp", "live", "assistant", filler); err != nil {
				t.Fatal(err)
			}
		}
		cfg := &config.Config{Model: config.Model{BaseURL: baseURL, Name: modelName}}
		a := agent.New(model.NewOpenAICompat(cfg.Model), store,
			tools.NewRegistry(tools.SaveMemory{K: kn}, tools.ForgetMemory{K: kn}),
			agent.SystemPrompt(cfg))
		a.WithKnowledge(kn)
		if _, err := a.Handle(ctx, "whatsapp", "live", "hola, seguimos con la charla"); err != nil {
			t.Fatal(err)
		}
		if _, err := kn.Append("preferences", lacón); err != nil {
			t.Fatal(err)
		}
		const q = "volviendo a lo de antes del todo: ¿cuál es mi comida favorita ahora mismo?"
		hits, err := kn.Recall(q)
		if err != nil {
			t.Fatal(err)
		}
		if !hasFact(hits, "lacón") {
			t.Fatalf("harness: recall did not surface the fact, probe invalid: %+v", hits)
		}
		reply, err := a.Handle(ctx, "whatsapp", "live", q)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("reply: %s", reply)
		if !strings.Contains(strings.ToLower(reply), "lacón") {
			t.Errorf("long-session recall-note miss: reply never used the recalled fact: %q", reply)
		}
	})

	t.Run("post-forget-abstention", func(t *testing.T) {
		kn := newKnowledge(t)
		if _, err := kn.Append("preferences", lacón); err != nil {
			t.Fatal(err)
		}
		a := newAgent(t, kn)
		if _, err := a.Handle(ctx, "whatsapp", "live", "olvida: comida favorita"); err != nil {
			t.Fatal(err)
		}
		hits, err := kn.Recall("¿cuál es mi comida favorita?")
		if err != nil {
			t.Fatal(err)
		}
		if hasFact(hits, "lacón") {
			t.Fatalf("harness: fact still recallable after olvida:, probe invalid: %+v", hits)
		}
		reply, err := a.Handle(ctx, "whatsapp", "live", "¿cuál es mi comida favorita?")
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("reply: %s", reply)
		if strings.Contains(strings.ToLower(reply), "lacón") {
			t.Errorf("erased fact stated back: %q", reply)
		}
		if !abstains(reply) {
			t.Errorf("no abstention after erase: %q", reply)
		}
	})
}
