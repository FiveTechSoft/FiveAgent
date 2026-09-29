package evals

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/sched"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

// Stage 22: cron scheduler - one-shot reminders and recurring jobs,
// persisted, delivered to the originating channel, auditable.

type fakeSender struct {
	mu   sync.Mutex
	sent []string
}

func (f *fakeSender) send(ctx context.Context, channel, userID, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, channel+"|"+userID+"|"+text)
	return nil
}
func (f *fakeSender) got() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.sent...)
}

// TestCronFiresOnce: a due one-shot fires exactly once, with the
// reminder prefix, and the done state survives a reload (no refire
// after a restart).
func TestCronFiresOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	fs := &fakeSender{}
	sc, err := sched.Open(path, fs.send)
	if err != nil {
		t.Fatal(err)
	}
	j, err := sc.Add(sched.Job{
		Channel: "whatsapp", UserID: "user1",
		Text: "sacar la ropa", NextRun: time.Now().Add(-time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	sc.RunOnce(context.Background())

	sent := fs.got()
	if len(sent) != 1 {
		t.Fatalf("expected exactly 1 delivery, got %v", sent)
	}
	if sent[0] != "whatsapp|user1|"+sched.ReminderPrefix+"sacar la ropa" {
		t.Fatalf("delivery text: %q", sent[0])
	}

	// Audit: the job records what ran and when.
	var job sched.Job
	for _, c := range sc.Jobs() {
		if c.ID == j.ID {
			job = c
		}
	}
	if !job.Done || job.RunCount != 1 || job.LastRun == nil {
		t.Fatalf("audit trail wrong: %+v", job)
	}

	// A second tick does not refire.
	sc.RunOnce(context.Background())
	if len(fs.got()) != 1 {
		t.Fatalf("one-shot refired on the next tick: %v", fs.got())
	}

	// A restart (reload from the file) does not refire either.
	sc2, err := sched.Open(path, fs.send)
	if err != nil {
		t.Fatal(err)
	}
	sc2.RunOnce(context.Background())
	if len(fs.got()) != 1 {
		t.Fatalf("one-shot refired after a restart: %v", fs.got())
	}
}

// TestCronRecurring: an interval job fires, stays alive, and its next
// run moves one interval ahead - so it is NOT due again immediately.
func TestCronRecurring(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	fs := &fakeSender{}
	sc, err := sched.Open(path, fs.send)
	if err != nil {
		t.Fatal(err)
	}
	_, err = sc.Add(sched.Job{
		Channel: "telegram", UserID: "user2",
		Text: "informe diario", NextRun: time.Now().Add(-time.Minute), Every: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	sc.RunOnce(context.Background())
	if len(fs.got()) != 1 {
		t.Fatalf("recurring job did not fire: %v", fs.got())
	}
	jobs := sc.Jobs()
	if len(jobs) != 1 || jobs[0].Done {
		t.Fatalf("recurring job must stay alive: %+v", jobs)
	}
	if d := time.Until(jobs[0].NextRun); d < 55*time.Minute || d > 61*time.Minute {
		t.Fatalf("next run must move one interval ahead, in %s", d)
	}
	// Not due again right away.
	sc.RunOnce(context.Background())
	if len(fs.got()) != 1 {
		t.Fatalf("recurring job refired before its interval: %v", fs.got())
	}
}

// TestCronScheduleTool: the model-facing tool registers the job for
// the current channel/user and confirms; bad timing args are refused.
func TestCronScheduleTool(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	fs := &fakeSender{}
	sc, err := sched.Open(path, fs.send)
	if err != nil {
		t.Fatal(err)
	}
	tool := tools.ScheduleJob{Sched: sc}
	ctx := tools.WithRequestInfo(context.Background(), "whatsapp", "user1")

	out, err := tool.Execute(ctx, json.RawMessage(`{"text":"sacar la ropa","in_minutes":5}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "reminder registered") || !strings.Contains(out, "sacar la ropa") {
		t.Fatalf("confirmation: %q", out)
	}
	jobs := sc.Jobs()
	if len(jobs) != 1 || jobs[0].Channel != "whatsapp" || jobs[0].UserID != "user1" || jobs[0].Text != "sacar la ropa" {
		t.Fatalf("job registered wrong: %+v", jobs)
	}
	if d := time.Until(jobs[0].NextRun); d < 4*time.Minute || d > 6*time.Minute {
		t.Fatalf("fires in %s, expected ~5 minutes", d)
	}

	// Recurring via the tool.
	if _, err := tool.Execute(ctx, json.RawMessage(`{"text":"backup","every_minutes":120}`)); err != nil {
		t.Fatal(err)
	}
	// Refusals: no timing, both timings, unparseable time.
	for _, bad := range []string{
		`{"text":"x"}`,
		`{"text":"x","in_minutes":5,"deliver_at":"2026-09-30T09:00:00+02:00"}`,
		`{"text":"x","deliver_at":"mañana por la mañana"}`,
	} {
		if _, err := tool.Execute(ctx, json.RawMessage(bad)); err == nil {
			t.Fatalf("bad args accepted: %s", bad)
		}
	}
	if len(sc.Jobs()) != 2 {
		t.Fatalf("refused calls must not register jobs: %+v", sc.Jobs())
	}
}
