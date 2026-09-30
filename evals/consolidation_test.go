package evals

// Stage 7l battery case ("idle-consolidation"): after a scripted set
// of conversations that seed near-duplicate facts, an idle pass merges
// them, the next recall returns the merged fact ONCE, and no fact is
// lost. The pass must fire on its own (idle timer), never inside a
// user turn.

import (
	"context"
	"net/http/httptest"
	"os"
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

func TestIdleConsolidation(t *testing.T) {
	dir := t.TempDir()
	kn, err := memory.OpenKnowledge(filepath.Join(dir, "memory"))
	if err != nil {
		t.Fatal(err)
	}
	// A scripted day of conversations seeds a near-duplicate: the
	// second bullet says everything the first says, and more.
	if _, err := kn.Append("people", "mi perro se llama Tobi"); err != nil {
		t.Fatal(err)
	}
	if _, err := kn.Append("people", "mi perro se llama Tobi y tiene 3 años"); err != nil {
		t.Fatal(err)
	}
	if _, err := kn.Append("preferences", "mi color favorito es verde musgo"); err != nil {
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
	a.WithConsolidation(80 * time.Millisecond)

	// One user turn, then the agent goes idle; the pass fires alone.
	if _, err := a.Handle(context.Background(), "whatsapp", "user-a", "hola"); err != nil {
		t.Fatal(err)
	}
	prefPath := filepath.Join(dir, "memory", "people.md")
	merged := false
	for i := 0; i < 60; i++ {
		time.Sleep(50 * time.Millisecond)
		raw, _ := os.ReadFile(prefPath)
		body := string(raw)
		if strings.Contains(body, "3 años") && !strings.Contains(body, "- mi perro se llama Tobi\n") {
			merged = true
			break
		}
	}
	if !merged {
		raw, _ := os.ReadFile(prefPath)
		t.Fatalf("idle pass never merged the duplicate:\n%s", raw)
	}
	// No fact lost: the surviving bullet keeps both the name and the age.
	raw, _ := os.ReadFile(prefPath)
	if !strings.Contains(string(raw), "Tobi") || !strings.Contains(string(raw), "3 años") {
		t.Fatalf("the merge lost a fact:\n%s", raw)
	}
	// Untouched files keep their facts byte for byte.
	if !memoryFilesContain(filepath.Join(dir, "memory"), "verde musgo") {
		t.Fatal("the pass touched an unrelated fact")
	}

	// The next recall returns the merged fact exactly once.
	if _, err := a.Handle(context.Background(), "whatsapp", "user-a", "¿cómo se llama mi perro?"); err != nil {
		t.Fatal(err)
	}
	mem := injectedMemory(t, p.last())
	if n := strings.Count(strings.ToLower(mem), "tobi"); n != 1 {
		t.Fatalf("recall returned the merged fact %d times, want 1:\n%s", n, mem)
	}
}
