package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/sched"
)

// ScheduleJob lets the model register a reminder or recurring
// automation for the current user (stage 22). The scheduler delivers
// the text back to the same channel when it fires.
type ScheduleJob struct{ Sched *sched.Scheduler }

func (t ScheduleJob) Name() string { return "schedule_job" }
func (t ScheduleJob) Description() string {
	return "Schedule a reminder or recurring automation for this user: the text is delivered back to this same chat when it fires. One-shot: pass in_minutes or deliver_at. Recurring: pass every_minutes. Use it whenever the user asks to be reminded of something or to run something on a schedule."
}
func (t ScheduleJob) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"text":{"type":"string","description":"what to deliver when the job fires, e.g. 'sacar la ropa de la lavadora'"},"in_minutes":{"type":"integer","description":"one-shot: fire this many minutes from now"},"deliver_at":{"type":"string","description":"one-shot: fire at this time, RFC3339 (2026-09-30T09:00:00+02:00) or '2006-01-02 15:04' local"},"every_minutes":{"type":"integer","description":"recurring: fire every this many minutes, first fire after one interval"}},"required":["text"]}`)
}

func (t ScheduleJob) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Text        string `json:"text"`
		InMinutes   int    `json:"in_minutes"`
		DeliverAt   string `json:"deliver_at"`
		EveryMinute int    `json:"every_minutes"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	channel, userID := RequestInfo(ctx)
	if userID == "" {
		return "", errors.New("schedule_job: no user in the request context")
	}
	now := time.Now()
	j := sched.Job{Channel: channel, UserID: userID, Text: a.Text}
	switch {
	case a.EveryMinute > 0:
		if a.InMinutes > 0 || a.DeliverAt != "" {
			return "", errors.New("schedule_job: every_minutes does not combine with in_minutes or deliver_at")
		}
		j.Every = a.EveryMinute
		j.NextRun = now.Add(time.Duration(a.EveryMinute) * time.Minute)
	case a.InMinutes > 0:
		if a.DeliverAt != "" {
			return "", errors.New("schedule_job: pass either in_minutes or deliver_at, not both")
		}
		j.NextRun = now.Add(time.Duration(a.InMinutes) * time.Minute)
	case a.DeliverAt != "":
		at, err := time.Parse(time.RFC3339, a.DeliverAt)
		if err != nil {
			at, err = time.ParseInLocation("2006-01-02 15:04", a.DeliverAt, time.Local)
			if err != nil {
				return "", fmt.Errorf("schedule_job: cannot parse deliver_at %q - use RFC3339 or '2006-01-02 15:04'", a.DeliverAt)
			}
		}
		j.NextRun = at
	default:
		return "", errors.New("schedule_job: when should it fire? pass in_minutes, deliver_at or every_minutes")
	}
	added, err := t.Sched.Add(j)
	if err != nil {
		return "", err
	}
	when := added.NextRun.Local().Format("2006-01-02 15:04")
	if added.Every > 0 {
		return fmt.Sprintf("recurring job registered: %q, every %d minutes, first fire at %s", added.Text, added.Every, when), nil
	}
	return fmt.Sprintf("reminder registered: %q, fires at %s", added.Text, when), nil
}
