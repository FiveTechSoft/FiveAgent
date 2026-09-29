package evals

// Stage 36 battery case ("unified"): one user writes from two
// channels; after the explicit linking flow the second channel's
// turn sees the first channel's context and memory scope - and an
// unlinked sender stays separate.

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/agent"
	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/identity"
	"github.com/FiveTechSoft/FiveAgent/internal/memory"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

func TestUnifiedBattery(t *testing.T) {
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
	ids, err := identity.Open(filepath.Join(dir, "identities.json"))
	if err != nil {
		t.Fatal(err)
	}
	a.WithIdentities(ids)
	ctx := context.Background()

	// The explicit linking flow: a code minted on whatsapp, redeemed
	// from telegram - mechanically, no model turn.
	code, err := ids.NewCode("whatsapp", "user-a")
	if err != nil {
		t.Fatal(err)
	}
	before := len(p.lastRequest.ch)
	reply, err := a.Handle(ctx, "telegram", "user-b", "vincular "+code)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(p.lastRequest.ch) - before; got != 0 {
		t.Fatalf("redemption reached the model (%d calls)", got)
	}
	if !strings.Contains(reply, "vinculados") {
		t.Fatalf("redemption not confirmed: %s", reply)
	}

	// The user writes from whatsapp with a fact; no save_memory, no
	// "recuerda:".
	if _, err := a.Handle(ctx, "whatsapp", "user-a",
		"te cuento algo de mi: mi equipo favorito es el Celta de Vigo desde siempre"); err != nil {
		t.Fatal(err)
	}
	a.DrainIndexer(ctx)

	// The same user writes from telegram: the turn sees the
	// whatsapp context (shared history) and the indexed fact
	// (shared memory scope).
	if _, err := a.Handle(ctx, "telegram", "user-b", "cual es mi equipo favorito"); err != nil {
		t.Fatal(err)
	}
	last := p.last()
	if !strings.Contains(last, "mi equipo favorito es el Celta de Vigo desde siempre") {
		t.Fatalf("second channel did not see the first channel's history:\n%s", last)
	}
	if !strings.Contains(last, "Celta de Vigo") {
		t.Fatalf("second channel did not see the first channel's memory scope:\n%s", last)
	}

	// An unlinked sender on the SAME second channel stays separate:
	// neither the history nor the fact.
	if _, err := a.Handle(ctx, "telegram", "user-c", "cual es mi equipo favorito"); err != nil {
		t.Fatal(err)
	}
	// No background work outlives the test (the 7n race lesson).
	a.DrainIndexer(ctx)
	other := p.last()
	if strings.Contains(other, "Celta") {
		t.Fatalf("unlinked sender saw the linked user's context:\n%s", other)
	}
}
