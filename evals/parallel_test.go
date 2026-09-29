package evals

// Stage 34 battery case ("parallel"): the model fans three
// independent subtasks out with ONE run_subtasks call; the fake
// model sleeps 250ms per subturn, so wall time proves real overlap
// (sequential would take ~750ms), the coordinator composes the
// answer, and delegation tools never reach the subturns.

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/agent"
	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/memory"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

func TestParallelBattery(t *testing.T) {
	dir := t.TempDir()
	kn, err := memory.OpenKnowledge(filepath.Join(dir, "memory"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := memory.OpenJSON(filepath.Join(dir, "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	p := newPlayer()
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	cfg := &config.Config{Model: config.Model{BaseURL: srv.URL, Name: "eval"}}
	mdl := model.NewOpenAICompat(cfg.Model)
	a := agent.New(mdl, store,
		tools.NewRegistry(tools.SaveMemory{K: kn}, tools.ForgetMemory{K: kn}),
		agent.SystemPrompt(cfg))
	a.WithKnowledge(kn)

	start := time.Now()
	reply, err := a.Handle(context.Background(), "whatsapp", "user-a",
		"paralelo: haz las tres tareas independientes")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if reply != "listo" {
		t.Fatalf("coordinator did not compose: %q", reply)
	}
	// Sequential: 3 x 250ms = 750ms. Parallel (cap 4): ~250ms.
	if elapsed > 600*time.Millisecond {
		t.Fatalf("no real overlap: %s for 3 x 250ms subturns", elapsed)
	}

	// The second main-turn request carries the tool result with all
	// three slots aggregated.
	reqs := p.drainAll()
	if len(reqs) < 5 { // main call + 3 subturns + main continuation
		t.Fatalf("expected at least 5 model calls, got %d", len(reqs))
	}
	last := reqs[len(reqs)-1]
	for _, slot := range []string{"=== Subtask 1", "=== Subtask 2", "=== Subtask 3"} {
		if !strings.Contains(last, slot) {
			t.Fatalf("coordinator missing a result slot %q:\n%s", slot, last)
		}
	}
	// Depth stays capped at 1: no subturn request offered the
	// delegation tools.
	for _, req := range reqs {
		if strings.Contains(req, "solving ONE small subtask") {
			if strings.Contains(req, `"run_subtasks"`) || strings.Contains(req, `"run_subtask"`) {
				t.Fatalf("delegation tool leaked into a subturn:\n%s", req)
			}
		}
	}
}
