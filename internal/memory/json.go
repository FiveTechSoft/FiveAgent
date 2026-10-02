package memory

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// record is one stored message.
type record struct {
	Channel   string    `json:"channel"`
	UserID    string    `json:"user_id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// jsonStore is a file-backed Store for local prototypes.
type jsonStore struct {
	mu   sync.Mutex
	path string
	msgs []record
}

// OpenJSON loads the JSON history file at path, creating it on first run.
func OpenJSON(path string) (Store, error) {
	s := &jsonStore{path: path}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &s.msgs); err != nil {
			return nil, err
		}
	}
	// Migration: drop stored system messages. The system prompt is
	// prepended at request time from the current code/config, so old
	// prompts must not survive in memory.
	kept := s.msgs[:0]
	dropped := false
	for _, m := range s.msgs {
		if m.Role == "system" {
			dropped = true
			continue
		}
		kept = append(kept, m)
	}
	s.msgs = kept
	if dropped {
		if err := s.save(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Append records one message and persists the whole file.
func (s *jsonStore) Append(ctx context.Context, channel, userID, role, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, record{
		Channel:   channel,
		UserID:    userID,
		Role:      role,
		Content:   content,
		CreatedAt: time.Now(),
	})
	return s.save()
}

// Recent returns the latest messages for a user, oldest first.
func (s *jsonStore) Recent(ctx context.Context, channel, userID string, limit int) ([][2]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out [][2]string
	for _, m := range s.msgs {
		if m.Channel == channel && m.UserID == userID {
			out = append(out, [2]string{m.Role, m.Content})
		}
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

// Close is a no-op: nothing to release for a plain file.
func (s *jsonStore) Close() error { return nil }

// Scrub redacts words out of the stored messages of one conversation.
func (s *jsonStore) Scrub(_ context.Context, channel, userID string, words []string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(words) == 0 {
		return 0, nil
	}
	changed := 0
	for i := range s.msgs {
		if s.msgs[i].Channel != channel || s.msgs[i].UserID != userID {
			continue
		}
		if next, did := RedactWords(s.msgs[i].Content, words); did {
			s.msgs[i].Content = next
			changed++
		}
	}
	if changed == 0 {
		return 0, nil
	}
	return changed, s.save()
}

// save writes the full array atomically (temp file + rename). Caller holds mu.
func (s *jsonStore) save() error {
	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(s.msgs, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
