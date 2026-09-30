package evals

// Stage 7k battery case ("snapshot"): at session start the memory
// files ride the system prompt as a FROZEN snapshot; a mid-session
// save_memory hits the disk but never rewrites the prompt - the M5
// hard gate is the prefix staying byte-identical after the write,
// with the new fact arriving through the per-turn recall note. And a
// fresh session picks the fact up in its snapshot.

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

func systemPrefix(t *testing.T, body string) string {
	t.Helper()
	var req request
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) == 0 || req.Messages[0].Role != "system" {
		t.Fatal("first message is not the system prompt")
	}
	return req.Messages[0].Content
}

func TestFrozenSnapshot(t *testing.T) {
	dir := t.TempDir()
	kn, err := memory.OpenKnowledge(filepath.Join(dir, "memory"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kn.Append("preferences", "su comida favorita es el pulpo a la gallega"); err != nil {
		t.Fatal(err)
	}
	p := newPlayer()
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	cfg := &config.Config{Model: config.Model{BaseURL: srv.URL, Name: "eval"}}
	store, err := memory.OpenJSON(filepath.Join(dir, "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	a := agent.New(model.NewOpenAICompat(cfg.Model), store,
		tools.NewRegistry(tools.SaveMemory{K: kn}, tools.ForgetMemory{K: kn}),
		agent.SystemPrompt(cfg))
	a.WithKnowledge(kn)

	// Turn 1: the session starts; the snapshot rides the system prompt.
	if _, err := a.Handle(context.Background(), "whatsapp", "user-a", "hola"); err != nil {
		t.Fatal(err)
	}
	prefix1 := systemPrefix(t, p.last())
	if !strings.Contains(prefix1, "Long-term memory snapshot") || !strings.Contains(prefix1, "pulpo") {
		t.Fatalf("session-start snapshot missing from the prefix:\n%s", prefix1)
	}

	// Mid-session write through the deterministic recuerda: path.
	if _, err := a.Handle(context.Background(), "whatsapp", "user-a",
		"recuerda: mi postre favorito es la filloa"); err != nil {
		t.Fatal(err)
	}
	if !memoryFilesContain(filepath.Join(dir, "memory"), "filloa") {
		t.Fatal("mid-session write never reached the disk")
	}

	// Turn 2 (M5 hard gate): the prefix is byte-identical to turn 1's,
	// and the new fact arrives through the recall note, not the prefix.
	if _, err := a.Handle(context.Background(), "whatsapp", "user-a", "¿cuál es mi postre favorito?"); err != nil {
		t.Fatal(err)
	}
	last := p.last()
	if prefix2 := systemPrefix(t, last); prefix2 != prefix1 {
		t.Fatalf("M5 violated: the write rewrote the prefix.\nbefore:\n%s\nafter:\n%s", prefix1, prefix2)
	}
	if strings.Contains(prefix1, "filloa") {
		t.Fatal("the new fact leaked into the frozen prefix")
	}
	if mem := injectedMemory(t, last); !strings.Contains(mem, "filloa") {
		t.Fatalf("mid-session fact missing from the recall note:\n%s", mem)
	}

	// A fresh session snapshots the disk as it is: the fact is in.
	store2, err := memory.OpenJSON(filepath.Join(dir, "history-2.json"))
	if err != nil {
		t.Fatal(err)
	}
	a2 := agent.New(model.NewOpenAICompat(cfg.Model), store2,
		tools.NewRegistry(tools.SaveMemory{K: kn}, tools.ForgetMemory{K: kn}),
		agent.SystemPrompt(cfg))
	a2.WithKnowledge(kn)
	if _, err := a2.Handle(context.Background(), "whatsapp", "user-a", "hola de nuevo"); err != nil {
		t.Fatal(err)
	}
	if prefix3 := systemPrefix(t, p.last()); !strings.Contains(prefix3, "filloa") {
		t.Fatalf("fresh-session snapshot lacks the mid-session fact:\n%s", prefix3)
	}
}
