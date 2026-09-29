package proactive

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeSink struct {
	replies []string
	runErr  error
}

func (f *fakeSink) run(ctx context.Context, ch, uid, prompt string) (string, error) {
	if f.runErr != nil {
		return "", f.runErr
	}
	return "reply to: " + prompt, nil
}

func (f *fakeSink) deliver(ctx context.Context, ch, uid, text string) error {
	f.replies = append(f.replies, text)
	return nil
}

func open(t *testing.T, f *fakeSink) *Manager {
	t.Helper()
	m, err := Open(filepath.Join(t.TempDir(), "subs.json"), f.run, f.deliver)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func sub(t *testing.T, m *Manager, s Subscription) *Subscription {
	t.Helper()
	s.Channel, s.UserID = "whatsapp", "user-a"
	if s.Source == "" {
		s.Source = "gmail"
	}
	if s.Instruction == "" {
		s.Instruction = "avisa al usuario"
	}
	out, err := m.Subscribe(s)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSubscribeValidationAndPersistence(t *testing.T) {
	f := &fakeSink{}
	m := open(t, f)
	if _, err := m.Subscribe(Subscription{Channel: "whatsapp", UserID: "user-a", Instruction: "x"}); err == nil {
		t.Fatal("empty source accepted")
	}
	if _, err := m.Subscribe(Subscription{Channel: "whatsapp", UserID: "user-a", Source: "gmail"}); err == nil {
		t.Fatal("empty instruction accepted")
	}
	s := sub(t, m, Subscription{Filter: "factura"})
	if s.ID == "" || s.CreatedAt.IsZero() {
		t.Fatalf("not stamped: %+v", s)
	}
	// Reopen: the subscription survives.
	m2, err := Open(m.path, f.run, f.deliver)
	if err != nil {
		t.Fatal(err)
	}
	got := m2.List()
	if len(got) != 1 || got[0].ID != s.ID || got[0].Filter != "factura" {
		t.Fatalf("not persisted: %+v", got)
	}
}

func TestNotifyMatchFilterAndDedup(t *testing.T) {
	f := &fakeSink{}
	m := open(t, f)
	sub(t, m, Subscription{Filter: "FACTURA"})
	ctx := context.Background()

	// Filter mismatch: no fire.
	if n := m.Notify(ctx, Event{Source: "gmail", ID: "e0", Summary: "newsletter semanal"}); n != 0 {
		t.Fatalf("filter mismatch fired")
	}
	// Source mismatch: no fire.
	if n := m.Notify(ctx, Event{Source: "calendar", ID: "e1", Summary: "factura"}); n != 0 {
		t.Fatalf("source mismatch fired")
	}
	// Match is case-insensitive on both filter and summary.
	if n := m.Notify(ctx, Event{Source: "gmail", ID: "e2", Summary: "Factura de Acme"}); n != 1 {
		t.Fatalf("match did not fire")
	}
	if len(f.replies) != 1 || !strings.Contains(f.replies[0], "Factura de Acme") {
		t.Fatalf("reply missing event content: %v", f.replies)
	}
	// Identical event: no double fire, no double delivery.
	if n := m.Notify(ctx, Event{Source: "gmail", ID: "e2", Summary: "Factura de Acme"}); n != 0 {
		t.Fatalf("identical event re-fired")
	}
	if len(f.replies) != 1 {
		t.Fatalf("double delivery: %v", f.replies)
	}
	// A different event id fires again.
	if n := m.Notify(ctx, Event{Source: "gmail", ID: "e3", Summary: "otra factura"}); n != 1 {
		t.Fatalf("new event did not fire")
	}
	// Audit: two fires recorded with their event ids.
	got := m.List()
	if len(got[0].Fires) != 2 || got[0].Fires[0].EventID != "e2" || got[0].Fires[1].EventID != "e3" {
		t.Fatalf("audit wrong: %+v", got[0].Fires)
	}
}

func TestPauseResumeAndRemove(t *testing.T) {
	f := &fakeSink{}
	m := open(t, f)
	s := sub(t, m, Subscription{})
	ctx := context.Background()
	if err := m.SetPaused(s.ID, true); err != nil {
		t.Fatal(err)
	}
	if n := m.Notify(ctx, Event{Source: "gmail", ID: "p1", Summary: "x"}); n != 0 {
		t.Fatal("paused subscription fired")
	}
	if err := m.SetPaused(s.ID, false); err != nil {
		t.Fatal(err)
	}
	if n := m.Notify(ctx, Event{Source: "gmail", ID: "p1", Summary: "x"}); n != 1 {
		t.Fatal("resumed subscription did not fire")
	}
	if err := m.Remove(s.ID); err != nil {
		t.Fatal(err)
	}
	if n := m.Notify(ctx, Event{Source: "gmail", ID: "p2", Summary: "x"}); n != 0 {
		t.Fatal("removed subscription fired")
	}
	if err := m.SetPaused("nope", true); err == nil {
		t.Fatal("pause on missing id accepted")
	}
}

func TestExpiry(t *testing.T) {
	f := &fakeSink{}
	m := open(t, f)
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	m.SetNow(func() time.Time { return now })
	sub(t, m, Subscription{ExpiresAt: now.Add(time.Hour)})
	ctx := context.Background()
	if n := m.Notify(ctx, Event{Source: "gmail", ID: "x1", Summary: "x"}); n != 1 {
		t.Fatal("live subscription did not fire")
	}
	// Time passes beyond the expiry: marked Done, never fires again.
	m.SetNow(func() time.Time { return now.Add(2 * time.Hour) })
	if n := m.Notify(ctx, Event{Source: "gmail", ID: "x2", Summary: "x"}); n != 0 {
		t.Fatal("expired subscription fired")
	}
	got := m.List()
	if !got[0].Done {
		t.Fatal("expired subscription not marked done")
	}
	// List returns copies: mutating the view does not touch the
	// manager's state.
	got[0].Done = false
	if !m.List()[0].Done {
		t.Fatal("List does not return copies")
	}
}

func TestRunnerErrorStaysRetryable(t *testing.T) {
	f := &fakeSink{runErr: errors.New("model down")}
	m := open(t, f)
	sub(t, m, Subscription{})
	ctx := context.Background()
	ev := Event{Source: "gmail", ID: "r1", Summary: "x"}
	if n := m.Notify(ctx, ev); n != 0 {
		t.Fatal("failing turn counted as fired")
	}
	if len(m.List()[0].Fires) != 0 {
		t.Fatal("failed turn recorded as a fire")
	}
	// The event was not marked seen: once the runner recovers, the
	// same event retries.
	f.runErr = nil
	if n := m.Notify(ctx, ev); n != 1 {
		t.Fatal("redelivered event did not retry")
	}
}
