package evals

// Stage 35 battery case ("proactive"): a scripted source event wakes
// the agent - no user message anywhere - the turn runs, and the reply
// lands through the delivery ledger. A second identical event does
// not double-deliver.

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/agent"
	"github.com/FiveTechSoft/FiveAgent/internal/channel"
	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/memory"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/proactive"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

func TestProactiveBattery(t *testing.T) {
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

	// The reply goes out through the real delivery ledger (stage
	// 19): obligation persisted BEFORE the send, delivered after.
	ledger, err := channel.OpenLedger(filepath.Join(dir, "delivery.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sent []string
	deliver := func(ctx context.Context, ch, uid, text string) error {
		d, err := ledger.Add(ch, uid, text)
		if err != nil {
			return err
		}
		ledger.Attempting(d.ID)
		sent = append(sent, text) // the fake platform send
		ledger.Delivered(d.ID)
		return nil
	}
	runner := func(ctx context.Context, ch, uid, prompt string) (string, error) {
		return a.Handle(ctx, ch, uid, prompt)
	}
	pm, err := proactive.Open(filepath.Join(dir, "subs.json"), runner, deliver)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pm.Subscribe(proactive.Subscription{
		Channel: "whatsapp", UserID: "user-a",
		Source: "gmail", Filter: "factura",
		Instruction: "avisa al usuario de la factura que acaba de llegar",
	}); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	before := len(p.lastRequest.ch)
	if n := pm.Notify(ctx, proactive.Event{Source: "gmail", ID: "msg-1", Summary: "factura de Acme por 420 EUR"}); n != 1 {
		t.Fatalf("subscription did not fire")
	}

	// The wake came from the event, not a user message: the model
	// saw exactly one new request, carrying the wake marker and the
	// event summary.
	if got := len(p.lastRequest.ch) - before; got != 1 {
		t.Fatalf("expected 1 model call, got %d", got)
	}
	last := p.last()
	if !strings.Contains(last, proactive.WakePrefix) || !strings.Contains(last, "factura de Acme por 420 EUR") {
		t.Fatalf("wake not marked as event-originated:\n%s", last)
	}
	// The reply landed through the ledger: one send, recorded as
	// delivered in the ledger file.
	if len(sent) != 1 || !strings.Contains(sent[0], "respuesta") {
		t.Fatalf("reply not delivered: %v", sent)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "delivery.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"delivered"`) {
		t.Fatalf("ledger did not record the delivery:\n%s", raw)
	}
	// Audit: the subscription records what it triggered.
	subs := pm.List()
	if len(subs) != 1 || len(subs[0].Fires) != 1 || subs[0].Fires[0].EventID != "msg-1" {
		t.Fatalf("audit wrong: %+v", subs)
	}

	// A second identical event: no new turn, no double delivery.
	before = len(p.lastRequest.ch)
	if n := pm.Notify(ctx, proactive.Event{Source: "gmail", ID: "msg-1", Summary: "factura de Acme por 420 EUR"}); n != 0 {
		t.Fatalf("identical event re-fired")
	}
	if got := len(p.lastRequest.ch) - before; got != 0 {
		t.Fatalf("identical event caused a model call")
	}
	if len(sent) != 1 {
		t.Fatalf("double delivery: %v", sent)
	}
}
