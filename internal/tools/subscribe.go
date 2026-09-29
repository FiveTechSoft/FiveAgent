package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/proactive"
)

// Subscribe lets the model register a source subscription for the
// current user (stage 35): when a matching event arrives at the
// source, the agent wakes on its own, runs a turn with the
// instruction, and the reply lands through the delivery ledger.
type Subscribe struct{ Subs *proactive.Manager }

func (t Subscribe) Name() string { return "subscribe" }
func (t Subscribe) Description() string {
	return "Subscribe this user to a source so the agent wakes on its own when something happens (an email arrives, a document changes, a calendar event starts). The instruction says what to do when it fires; the reply arrives in this same chat. For time-based reminders use schedule_job instead."
}
func (t Subscribe) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"source":{"type":"string","description":"the source to watch, e.g. 'gmail', 'calendar', 'drive'"},"filter":{"type":"string","description":"optional: only events whose summary contains this text (case-insensitive) fire"},"instruction":{"type":"string","description":"what the agent should do when the subscription fires"},"ttl_minutes":{"type":"integer","description":"optional: the subscription expires this many minutes from now; omit for no expiry"}},"required":["source","instruction"]}`)
}

func (t Subscribe) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Source      string `json:"source"`
		Filter      string `json:"filter"`
		Instruction string `json:"instruction"`
		TTLMinutes  int    `json:"ttl_minutes"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	channel, userID := RequestInfo(ctx)
	if userID == "" {
		return "", errors.New("subscribe: no user in the request context")
	}
	s := proactive.Subscription{
		Channel:     channel,
		UserID:      userID,
		Source:      a.Source,
		Filter:      a.Filter,
		Instruction: a.Instruction,
	}
	if a.TTLMinutes > 0 {
		s.ExpiresAt = time.Now().Add(time.Duration(a.TTLMinutes) * time.Minute)
	}
	added, err := t.Subs.Subscribe(s)
	if err != nil {
		return "", err
	}
	out := fmt.Sprintf("subscribed to %s (id %s)", added.Source, added.ID)
	if added.Filter != "" {
		out += fmt.Sprintf(", filter %q", added.Filter)
	}
	if !added.ExpiresAt.IsZero() {
		out += ", expires " + added.ExpiresAt.Local().Format("2006-01-02 15:04")
	}
	return out, nil
}

// Subscriptions lists the current user's subscriptions with their
// audit trail (stage 35).
type Subscriptions struct{ Subs *proactive.Manager }

func (t Subscriptions) Name() string { return "subscriptions" }
func (t Subscriptions) Description() string {
	return "List this user's source subscriptions: what each one watches, its filter and instruction, whether it is paused or expired, and what it has triggered."
}
func (t Subscriptions) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

func (t Subscriptions) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	_, userID := RequestInfo(ctx)
	if userID == "" {
		return "", errors.New("subscriptions: no user in the request context")
	}
	var b strings.Builder
	n := 0
	for _, s := range t.Subs.List() {
		if s.UserID != userID {
			continue
		}
		n++
		state := "active"
		if s.Done {
			state = "expired"
		} else if s.Paused {
			state = "paused"
		}
		fmt.Fprintf(&b, "- %s: %s", s.ID, s.Source)
		if s.Filter != "" {
			fmt.Fprintf(&b, " (filter %q)", s.Filter)
		}
		fmt.Fprintf(&b, " - %s, %d fires", state, len(s.Fires))
		if !s.ExpiresAt.IsZero() && !s.Done {
			fmt.Fprintf(&b, ", expires %s", s.ExpiresAt.Local().Format("2006-01-02 15:04"))
		}
		if len(s.Fires) > 0 {
			last := s.Fires[len(s.Fires)-1]
			fmt.Fprintf(&b, ", last: %s (%s)", last.Summary, last.At.Local().Format("2006-01-02 15:04"))
		}
		b.WriteString("\n")
	}
	if n == 0 {
		return "no subscriptions", nil
	}
	return b.String(), nil
}

// PauseSubscription pauses or resumes one of the current user's
// subscriptions (stage 35).
type PauseSubscription struct{ Subs *proactive.Manager }

func (t PauseSubscription) Name() string { return "pause_subscription" }
func (t PauseSubscription) Description() string {
	return "Pause or resume one of this user's source subscriptions. A paused subscription stays on the list but does not fire."
}
func (t PauseSubscription) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","description":"the subscription id"},"paused":{"type":"boolean","description":"true to pause, false to resume"}},"required":["id","paused"]}`)
}

func (t PauseSubscription) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		ID     string `json:"id"`
		Paused bool   `json:"paused"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	if !ownsSubscription(t.Subs, ctx, a.ID) {
		return "", errors.New("pause_subscription: not one of this user's subscriptions")
	}
	if err := t.Subs.SetPaused(a.ID, a.Paused); err != nil {
		return "", err
	}
	if a.Paused {
		return "subscription paused", nil
	}
	return "subscription resumed", nil
}

// Unsubscribe removes one of the current user's subscriptions
// (stage 35).
type Unsubscribe struct{ Subs *proactive.Manager }

func (t Unsubscribe) Name() string { return "unsubscribe" }
func (t Unsubscribe) Description() string {
	return "Remove one of this user's source subscriptions; it never fires again."
}
func (t Unsubscribe) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","description":"the subscription id"}},"required":["id"]}`)
}

func (t Unsubscribe) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	if !ownsSubscription(t.Subs, ctx, a.ID) {
		return "", errors.New("unsubscribe: not one of this user's subscriptions")
	}
	if err := t.Subs.Remove(a.ID); err != nil {
		return "", err
	}
	return "subscription removed", nil
}

// ownsSubscription guards cross-user changes: a user only pauses or
// removes their own subscriptions.
func ownsSubscription(m *proactive.Manager, ctx context.Context, id string) bool {
	_, userID := RequestInfo(ctx)
	for _, s := range m.List() {
		if s.ID == id {
			return s.UserID == userID
		}
	}
	return false
}
