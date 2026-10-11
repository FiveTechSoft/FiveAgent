package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/memory"
)

func consentRig(t *testing.T, name string, on bool) (*Agent, *permissionProbe) {
	t.Helper()
	a, probe := permissionRig(t, name, true)
	st, err := memory.OpenJSON(filepath.Join(t.TempDir(), "h.json"))
	if err != nil {
		t.Fatal(err)
	}
	a.store = st
	if on {
		a.WithEffectConfirmation()
	}
	return a, probe
}

func TestEffectNeedsConfirmation(t *testing.T) {
	ctx := context.Background()
	a, probe := consentRig(t, "gmail_send", true)
	if _, err := a.Handle(ctx, "wa", "u1", "manda el correo"); err != nil {
		t.Fatal(err)
	}
	if n := probe.calls.Load(); n != 0 {
		t.Fatalf("effect ran without confirmation: %d", n)
	}
	// A different sender saying yes must not approve u1's pending call.
	if _, err := a.Handle(ctx, "wa", "u2", "sí"); err != nil {
		t.Fatal(err)
	}
	if n := probe.calls.Load(); n != 0 {
		t.Fatalf("other user's yes approved the call: %d", n)
	}
	// An unrelated message clears the pending approval.
	a.Handle(ctx, "wa", "u1", "otra cosa")
	if n := probe.calls.Load(); n != 0 {
		t.Fatalf("unrelated turn ran effect: %d", n)
	}
}

func TestEffectRunsAfterYesOnce(t *testing.T) {
	ctx := context.Background()
	a, probe := consentRig(t, "gmail_send", true)
	a.Handle(ctx, "wa", "u1", "manda el correo")
	reply, err := a.Handle(ctx, "wa", "u1", "Sí")
	if err != nil {
		t.Fatal(err)
	}
	if n := probe.calls.Load(); n != 1 {
		t.Fatalf("approved call ran %d times, want 1 (reply %q)", n, reply)
	}
	// The approval is spent: asking again needs a new yes.
	a.Handle(ctx, "wa", "u1", "otra vez")
	a.Handle(ctx, "wa", "u1", "otra cosa")
	if n := probe.calls.Load(); n != 1 {
		t.Fatalf("spent approval reused: %d", n)
	}
}

func TestQueryToolsAndDefaultOffUnaffected(t *testing.T) {
	ctx := context.Background()
	a, probe := consentRig(t, "web_search", true)
	a.Handle(ctx, "wa", "u1", "busca")
	if probe.calls.Load() != 1 {
		t.Fatal("query tool must not need confirmation")
	}
	b, p2 := consentRig(t, "gmail_send", false)
	b.Handle(ctx, "wa", "u1", "manda")
	if p2.calls.Load() != 1 {
		t.Fatal("confirmation must be off unless enabled")
	}
	if !strings.Contains(callHash("a", []byte(`{"x":1,"y":2}`)), callHash("a", []byte(`{ "y":2, "x":1 }`))) {
		t.Fatal("hash must ignore key order and spacing")
	}
}

func TestAutomatedTurnNeitherApprovesNorClears(t *testing.T) {
	ctx := context.Background()
	auto := WithAutomated(ctx)
	a, probe := consentRig(t, "gmail_send", true)
	a.Handle(ctx, "wa", "u1", "manda el correo") // held, pending
	// A system wake with the text "sí" must not approve it...
	a.Handle(auto, "wa", "u1", "sí")
	if n := probe.calls.Load(); n != 0 {
		t.Fatalf("automated turn approved an effect: %d", n)
	}
	// ...and must not erase the pending approval either: the user's own
	// yes afterwards still works.
	a.Handle(ctx, "wa", "u1", "sí")
	if n := probe.calls.Load(); n != 1 {
		t.Fatalf("user yes after automated turn ran %d times, want 1", n)
	}
}

// The notice is written by the harness: whatever the model says after a
// held call, the reply tells the user something is waiting, and only
// then. Names the tool, not its arguments.
func TestHeldEffectAddsNoticeToReply(t *testing.T) {
	ctx := context.Background()
	a, probe := consentRig(t, "gmail_send", true)
	reply, err := a.Handle(ctx, "wa", "u1", "manda el correo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, "Pendiente de tu confirmación") || !strings.Contains(reply, "gmail_send") {
		t.Fatalf("held call must add the notice naming the tool: %q", reply)
	}
	if probe.calls.Load() != 0 {
		t.Fatal("effect ran")
	}
	// After the yes the call runs and no notice is added.
	reply, err = a.Handle(ctx, "wa", "u1", "sí")
	if err != nil {
		t.Fatal(err)
	}
	if probe.calls.Load() != 1 || strings.Contains(reply, "Pendiente de tu confirmación") {
		t.Fatalf("after yes: calls=%d reply=%q", probe.calls.Load(), reply)
	}
}

func TestNoNoticeWhenNothingHeld(t *testing.T) {
	ctx := context.Background()
	a, _ := consentRig(t, "gmail_send", false) // confirmation off
	reply, err := a.Handle(ctx, "wa", "u1", "manda el correo")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(reply, "Pendiente de tu confirmación") {
		t.Fatalf("notice without a held call: %q", reply)
	}
	q, _ := consentRig(t, "gmail_search", true) // query tool, never held
	reply, _ = q.Handle(ctx, "wa", "u1", "busca")
	if strings.Contains(reply, "Pendiente de tu confirmación") {
		t.Fatalf("notice for a query tool: %q", reply)
	}
}
