// Package proactive is stage 35: subscriptions to sources that wake
// the agent on their own. An adapter calls Notify when something
// happens (an email arrives, a document changes, a calendar event
// starts); every matching subscription runs one agent turn and the
// reply goes out through the delivery ledger (stage 19), so a crash
// loses nothing. Time-based wakes are NOT here: they ride the cron
// scheduler (stage 22) - this package only handles source events.
//
// Design, documented honestly:
//   - Every subscription is auditable: it carries its source, filter
//     and instruction, plus a bounded trail of what it triggered
//     (event id, summary, when - last 50).
//   - A subscription expires (ExpiresAt) or pauses cleanly; an
//     expired one is Done and never fires again.
//   - Dedup: each subscription remembers the last 50 event ids it
//     fired on; a source redelivering the same event never causes a
//     second turn or a second reply.
//   - A failed turn logs and the event stays unseen: if the source
//     redelivers, the wake retries. There is no other retry - a wake
//     is not a user waiting, and a failing subscription must not
//     loop. Once the turn ran, the fire is recorded even if delivery
//     fails: the ledger (stage 19) owns reply recovery from there.
package proactive

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// WakePrefix marks a turn that came from a source event, not from a
// user message - parallel to the scheduler's reminder prefix.
const WakePrefix = "🔔 "

// auditCap bounds the per-subscription fire trail and the seen-event
// dedup list. A subscription is a long-lived object; unbounded
// history would grow the file without limit.
const auditCap = 50

// Event is one thing that happened at a source. ID must be stable and
// unique per source - it is the dedup key.
type Event struct {
	Source  string `json:"source"`
	ID      string `json:"id"`
	Summary string `json:"summary"`
}

// FireRecord is the audit trail of one fired wake.
type FireRecord struct {
	At      time.Time `json:"at"`
	EventID string    `json:"event_id"`
	Summary string    `json:"summary"`
}

// Subscription wakes one user's agent when a matching source event
// arrives.
type Subscription struct {
	ID          string       `json:"id"`
	Channel     string       `json:"channel"`
	UserID      string       `json:"user_id"`
	Source      string       `json:"source"`
	Filter      string       `json:"filter,omitempty"` // case-insensitive substring over the event summary; empty matches all
	Instruction string       `json:"instruction"`
	CreatedAt   time.Time    `json:"created_at"`
	ExpiresAt   time.Time    `json:"expires_at,omitempty"` // zero: never expires
	Paused      bool         `json:"paused,omitempty"`
	Done        bool         `json:"done,omitempty"` // expired; final
	Fires       []FireRecord `json:"fires,omitempty"`
	Seen        []string     `json:"seen,omitempty"` // event ids already fired on (dedup), last auditCap
}

// Runner runs one agent turn and returns its reply.
type Runner func(ctx context.Context, channel, userID, prompt string) (string, error)

// Deliver sends the reply through the delivery ledger.
type Deliver func(ctx context.Context, channel, userID, text string) error

// Manager is a JSON-file-backed set of subscriptions. Open with Open;
// concurrent-safe.
type Manager struct {
	mu      sync.Mutex
	path    string
	subs    []*Subscription
	run     Runner
	deliver Deliver
	// now is a test hook; nil uses time.Now.
	now func() time.Time
}

// Open loads the subscriptions file at path, starting empty on first
// use.
func Open(path string, run Runner, deliver Deliver) (*Manager, error) {
	if run == nil || deliver == nil {
		return nil, errors.New("proactive: runner and deliver are required")
	}
	m := &Manager{path: path, run: run, deliver: deliver}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return m, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return m, nil
	}
	if err := json.Unmarshal(b, &m.subs); err != nil {
		return nil, fmt.Errorf("subscriptions file %s: %w", path, err)
	}
	return m, nil
}

// SetNow overrides the clock (tests).
func (m *Manager) SetNow(f func() time.Time) { m.now = f }

func (m *Manager) timeNow() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

// Subscribe registers a subscription and persists it. The caller
// fills Channel, UserID, Source, Instruction and optionally Filter
// and ExpiresAt; Subscribe stamps the rest.
func (m *Manager) Subscribe(s Subscription) (*Subscription, error) {
	if strings.TrimSpace(s.Source) == "" {
		return nil, errors.New("proactive: source is empty")
	}
	if strings.TrimSpace(s.Instruction) == "" {
		return nil, errors.New("proactive: instruction is empty")
	}
	if s.Channel == "" || s.UserID == "" {
		return nil, errors.New("proactive: channel and user are required")
	}
	var rnd [4]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return nil, err
	}
	s.ID = fmt.Sprintf("%d-%s", time.Now().UnixNano(), hex.EncodeToString(rnd[:]))
	s.CreatedAt = m.timeNow()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subs = append(m.subs, &s)
	if err := m.saveLocked(); err != nil {
		return nil, err
	}
	return &s, nil
}

// List returns a copy of every subscription, for audit. Expired ones
// are marked Done first (and the file updated), so the listing always
// tells the truth.
func (m *Manager) List() []Subscription {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked()
	out := make([]Subscription, 0, len(m.subs))
	for _, s := range m.subs {
		out = append(out, *s)
	}
	return out
}

// SetPaused pauses or resumes one subscription.
func (m *Manager) SetPaused(id string, paused bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.subs {
		if s.ID == id {
			s.Paused = paused
			return m.saveLocked()
		}
	}
	return fmt.Errorf("proactive: no subscription %s", id)
}

// Remove deletes one subscription.
func (m *Manager) Remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.subs {
		if s.ID == id {
			m.subs = append(m.subs[:i], m.subs[i+1:]...)
			return m.saveLocked()
		}
	}
	return fmt.Errorf("proactive: no subscription %s", id)
}

// Notify fires every subscription matching ev and returns how many
// fired. Matching: same source (case-insensitive), filter contained
// in the event summary (case-insensitive), not paused, not expired,
// and this exact event id not seen before.
func (m *Manager) Notify(ctx context.Context, ev Event) int {
	if ev.Source == "" || ev.ID == "" {
		log.Printf("proactive: event without source or id dropped: %+v", ev)
		return 0
	}
	m.mu.Lock()
	m.expireLocked()
	var due []*Subscription
	for _, s := range m.subs {
		if s.Done || s.Paused {
			continue
		}
		if !strings.EqualFold(s.Source, ev.Source) {
			continue
		}
		if s.Filter != "" && !strings.Contains(strings.ToLower(ev.Summary), strings.ToLower(s.Filter)) {
			continue
		}
		seen := false
		for _, id := range s.Seen {
			if id == ev.ID {
				seen = true
				break
			}
		}
		if !seen {
			due = append(due, s)
		}
	}
	m.mu.Unlock()

	fired := 0
	for _, s := range due {
		prompt := fmt.Sprintf("%s[%s] %s\n\nEvent: %s", WakePrefix, ev.Source, s.Instruction, ev.Summary)
		runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		reply, err := m.run(runCtx, s.Channel, s.UserID, prompt)
		cancel()
		if err != nil {
			// Not marked seen: a source redelivery retries the wake.
			log.Printf("proactive: subscription %s: turn failed: %v", s.ID, err)
			continue
		}
		deliverCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		derr := m.deliver(deliverCtx, s.Channel, s.UserID, reply)
		cancel()
		if derr != nil {
			// The turn ran; the ledger owns reply recovery.
			log.Printf("proactive: subscription %s: delivery error (ledger retries): %v", s.ID, derr)
		}
		m.mu.Lock()
		s.Seen = appendCapped(s.Seen, ev.ID)
		s.Fires = appendCapped(s.Fires, FireRecord{At: m.timeNow(), EventID: ev.ID, Summary: ev.Summary})
		if err := m.saveLocked(); err != nil {
			log.Printf("proactive: save: %v", err)
		}
		m.mu.Unlock()
		fired++
		log.Printf("proactive: subscription %s fired on %s/%s for %s/%s", s.ID, ev.Source, ev.ID, s.Channel, s.UserID)
	}
	return fired
}

// expireLocked marks expired subscriptions Done and persists the
// change once.
func (m *Manager) expireLocked() {
	now := m.timeNow()
	dirty := false
	for _, s := range m.subs {
		if !s.Done && !s.ExpiresAt.IsZero() && !s.ExpiresAt.After(now) {
			s.Done = true
			dirty = true
			log.Printf("proactive: subscription %s expired", s.ID)
		}
	}
	if dirty {
		if err := m.saveLocked(); err != nil {
			log.Printf("proactive: save: %v", err)
		}
	}
}

func appendCapped[T any](xs []T, x T) []T {
	xs = append(xs, x)
	if len(xs) > auditCap {
		xs = xs[len(xs)-auditCap:]
	}
	return xs
}

// saveLocked writes the subscriptions file atomically (temp file +
// rename).
func (m *Manager) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m.subs, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}
