// Package sched runs scheduled jobs (stage 22): one-shot reminders and
// recurring automations, persisted to a JSON file, delivered back to
// the originating channel, and auditable (every job records what ran,
// when, and what it sent).
package sched

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
	"sync"
	"time"
)

// ReminderPrefix marks a message that arrives from a scheduled job.
const ReminderPrefix = "⏰ "

// Job is one scheduled automation. A zero Every is a one-shot
// reminder; Every > 0 refires every that many minutes.
type Job struct {
	ID        string     `json:"id"`
	Channel   string     `json:"channel"`
	UserID    string     `json:"user_id"`
	Text      string     `json:"text"`
	NextRun   time.Time  `json:"next_run"`
	Every     int        `json:"every_minutes,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	LastRun   *time.Time `json:"last_run,omitempty"`
	RunCount  int        `json:"run_count"`
	Done      bool       `json:"done,omitempty"`
}

// Sender delivers one scheduled message to a channel user.
type Sender func(ctx context.Context, channel, userID, text string) error

// Scheduler is a JSON-file-backed job runner. Open with Open;
// concurrent-safe.
type Scheduler struct {
	mu   sync.Mutex
	path string
	jobs []*Job
	send Sender
	// tick is the fire-check interval; 0 defaults to 15 s.
	tick time.Duration
	// now is a test hook; nil uses time.Now.
	now func() time.Time
}

// Open loads the jobs file at path, starting empty on first use.
func Open(path string, send Sender) (*Scheduler, error) {
	s := &Scheduler{path: path, send: send, tick: 15 * time.Second}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(b, &s.jobs); err != nil {
		return nil, fmt.Errorf("jobs file %s: %w", path, err)
	}
	return s, nil
}

// SetTick overrides the fire-check interval (tests).
func (s *Scheduler) SetTick(d time.Duration) { s.tick = d }

// SetNow overrides the clock (tests).
func (s *Scheduler) SetNow(f func() time.Time) { s.now = f }

func (s *Scheduler) timeNow() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// Add registers a job and persists it. The caller fills Channel,
// UserID, Text, NextRun and optionally Every; Add stamps the rest.
func (s *Scheduler) Add(j Job) (*Job, error) {
	if j.Text == "" {
		return nil, errors.New("sched: job text is empty")
	}
	if j.NextRun.IsZero() {
		return nil, errors.New("sched: job has no next run")
	}
	var rnd [4]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return nil, err
	}
	j.ID = fmt.Sprintf("%d-%s", time.Now().UnixNano(), hex.EncodeToString(rnd[:]))
	j.CreatedAt = s.timeNow()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs = append(s.jobs, &j)
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return &j, nil
}

// Jobs returns a copy of every job, for audit.
func (s *Scheduler) Jobs() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, *j)
	}
	return out
}

// RunOnce fires every due job exactly once, in order. Exported so
// tests drive the scheduler without sleeping.
func (s *Scheduler) RunOnce(ctx context.Context) {
	s.mu.Lock()
	due := make([]*Job, 0, len(s.jobs))
	now := s.timeNow()
	for _, j := range s.jobs {
		if !j.Done && !j.NextRun.After(now) {
			due = append(due, j)
		}
	}
	s.mu.Unlock()

	for _, j := range due {
		sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := s.send(sendCtx, j.Channel, j.UserID, ReminderPrefix+j.Text)
		cancel()
		if err != nil {
			log.Printf("sched: job %s: delivery failed: %v (retried on the next tick)", j.ID, err)
			continue // stays due; retried
		}
		s.mu.Lock()
		fired := s.timeNow()
		j.LastRun = &fired
		j.RunCount++
		if j.Every > 0 {
			j.NextRun = fired.Add(time.Duration(j.Every) * time.Minute)
		} else {
			j.Done = true
		}
		if err := s.saveLocked(); err != nil {
			log.Printf("sched: save: %v", err)
		}
		s.mu.Unlock()
		log.Printf("sched: job %s fired to %s/%s (run %d)", j.ID, j.Channel, j.UserID, j.RunCount)
	}
}

// Run fires due jobs every tick until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(s.tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.RunOnce(ctx)
		}
	}
}

// saveLocked writes the jobs file atomically (temp file + rename).
func (s *Scheduler) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.jobs, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
