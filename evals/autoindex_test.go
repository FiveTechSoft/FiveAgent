package evals

// Stage 7n battery case ("auto-index"): a scripted conversation that
// mentions a fact - with no "recuerda:" anywhere and no save_memory
// call - recalls it in a later session, because the background
// indexer absorbed it. And one sender's indexed facts never surface
// for another sender.

import (
	"context"
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

func TestAutoIndexBattery(t *testing.T) {
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
	a.WithIndexer(agent.NewIndexer(mdl, filepath.Join(dir, "memory")))

	// A conversation that mentions a fact. No "recuerda:", and the
	// player's default branch never calls save_memory.
	if _, err := a.Handle(context.Background(), "whatsapp", "user-a",
		"te digo una cosa importante sobre mi: mi equipo favorito es el Celta de Vigo desde siempre"); err != nil {
		t.Fatal(err)
	}
	a.DrainIndexer(context.Background())

	// A later session: the recall block of the NEXT turn must carry
	// the fact - and it arrived through the indexer alone.
	if _, err := a.Handle(context.Background(), "whatsapp", "user-a", "cual es mi equipo favorito"); err != nil {
		t.Fatal(err)
	}
	mem := injectedMemory(t, p.last())
	if !strings.Contains(mem, "Celta de Vigo") {
		t.Fatalf("indexed fact missing from later recall:\n%s", mem)
	}

	// Isolation: another sender asking the same question gets no
	// trace of user A's fact.
	if _, err := a.Handle(context.Background(), "whatsapp", "user-b", "cual es mi equipo favorito"); err != nil {
		t.Fatal(err)
	}
	memB := injectedMemory(t, p.last())
	if strings.Contains(memB, "Celta") {
		t.Fatalf("sender A fact leaked into sender B recall:\n%s", memB)
	}

	// The fact never touched the global scope (deliberate curation
	// was never invoked in this test).
	if hits, _ := kn.Recall("Celta"); len(hits) > 0 {
		for _, h := range hits {
			for _, ln := range h.Lines {
				if strings.Contains(ln, "Celta de Vigo") {
					t.Fatalf("indexed fact in global scope: %s", ln)
				}
			}
		}
	}
}
