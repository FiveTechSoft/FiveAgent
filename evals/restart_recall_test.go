package evals

// Stage 7m CI proof of the runner's M2 mechanics: a restart_before
// prompt rebuilds the agent on a FRESH history store over the SAME
// memory dir, so the recall the model receives after the restart can
// only come from disk; and pad_turns filler turns really push the
// setup past the 20-message window. If the restart ever kept the
// history, or the padding never happened, this test fails.

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/agent"
	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/memory"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

func messageCount(t *testing.T, body string) int {
	t.Helper()
	var req request
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	return len(req.Messages)
}

func TestRestartRecallMechanics(t *testing.T) {
	dir := t.TempDir()
	kn, err := memory.OpenKnowledge(filepath.Join(dir, "memory"))
	if err != nil {
		t.Fatal(err)
	}
	p := newPlayer()
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	cfg := &config.Config{Model: config.Model{BaseURL: srv.URL, Name: "eval"}}
	build := func(st memory.Store) *agent.Agent {
		a := agent.New(model.NewOpenAICompat(cfg.Model), st,
			tools.NewRegistry(tools.SaveMemory{K: kn}, tools.ForgetMemory{K: kn}),
			agent.SystemPrompt(cfg))
		a.WithKnowledge(kn)
		return a
	}
	store1, err := memory.OpenJSON(filepath.Join(dir, "history-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	a := build(store1)
	// The deterministic recuerda: path writes the fact to disk.
	if _, err := a.Handle(context.Background(), "whatsapp", "user-a",
		"recuerda: mi plato de fiesta es la empanada de zamburiñas"); err != nil {
		t.Fatal(err)
	}
	if !memoryFilesContain(filepath.Join(dir, "memory"), "empanada") {
		t.Fatal("recuerda: setup never reached the disk")
	}
	// Pad: 12 filler turns (24 messages) push the setup past the
	// 20-message window the agent sends to the model.
	for i := 0; i < 12; i++ {
		if _, err := a.Handle(context.Background(), "whatsapp", "user-a", fillerPrompts[i]); err != nil {
			t.Fatal(err)
		}
	}
	hist, err := store1.Recent(context.Background(), "whatsapp", "user-a", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) < 25 {
		t.Fatalf("padding did not accumulate: %d messages", len(hist))
	}
	// Restart: fresh store, same knowledge - the runner's M2 restart.
	store2, err := memory.OpenJSON(filepath.Join(dir, "history-2.json"))
	if err != nil {
		t.Fatal(err)
	}
	a = build(store2)
	if _, err := a.Handle(context.Background(), "whatsapp", "user-a",
		"¿cuál es mi plato de fiesta?"); err != nil {
		t.Fatal(err)
	}
	last := p.last()
	mem := injectedMemory(t, last)
	if !strings.Contains(mem, "empanada") {
		t.Fatalf("post-restart recall lacks the fact; memory:\n%s", mem)
	}
	// Fresh session: the model saw a short conversation, not the 25+
	// padded messages. A restart that keeps the history fails here.
	if n := messageCount(t, last); n > 6 {
		t.Fatalf("post-restart request carried %d messages; the restart kept the history", n)
	}
	// And the fact arrives as data, not instructions: the recall block
	// is labeled long-term memory, injected by the recall path.
	if !strings.Contains(mem, "Long-term memory recall") {
		t.Fatal("fact did not arrive through the labeled recall block")
	}
}
