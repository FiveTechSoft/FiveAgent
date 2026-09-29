// Package identity is stage 36: one conversation per user across
// channels. Channel identities (whatsapp/346..., telegram/123...)
// resolve to ONE user identity through an explicit linking flow -
// never guessed, never inferred from a phone number or a name. An
// unlinked sender resolves to itself, so separate users stay
// separate by default.
//
// The linking flow, documented honestly:
//   - On channel A the user asks to link; the agent's link_channel
//     tool creates a one-time code bound to A's identity (10-minute
//     expiry).
//   - From channel B the user writes "vincular <code>"; the agent
//     redeems it mechanically (no model turn) and B's identity maps
//     to A's. The code is deleted on redemption.
//   - The canonical identity is the one that created the code. A
//     code created by an already-linked identity points at its
//     canonical, so chains resolve to one root.
//   - History and the auto-indexed memory scope key off the
//     canonical identity; delivery stays per-channel (each reply
//     lands on the channel the user wrote from - that is the
//     adapter's job, unchanged).
package identity

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// codeTTL bounds a link code's life: long enough to switch channels,
// short enough that a leaked code goes stale fast.
const codeTTL = 10 * time.Minute

// LinkPrefix starts a redemption message; the agent intercepts it
// mechanically, parallel to "recuerda:".
const LinkPrefix = "vincular "

// linkCode is a pending one-time link invitation.
type linkCode struct {
	Code      string    `json:"code"`
	Owner     string    `json:"owner"` // identity key that created it
	ExpiresAt time.Time `json:"expires_at"`
}

// storeFile is the on-disk shape.
type storeFile struct {
	Links map[string]string `json:"links"` // identity key -> canonical identity key
	Codes []linkCode        `json:"codes,omitempty"`
}

// Key builds the identity key for a channel user.
func Key(channel, userID string) string { return channel + "/" + userID }

// Store is a JSON-file-backed set of identity links and pending
// codes. Open with Open; concurrent-safe.
type Store struct {
	mu    sync.Mutex
	path  string
	links map[string]string
	codes []linkCode
	// now is a test hook; nil uses time.Now.
	now func() time.Time
}

// Open loads the identity file at path, starting empty on first use.
func Open(path string) (*Store, error) {
	s := &Store{path: path, links: map[string]string{}}
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
	var f storeFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("identity file %s: %w", path, err)
	}
	for k, v := range f.Links {
		s.links[k] = v
	}
	s.codes = f.Codes
	return s, nil
}

// SetNow overrides the clock (tests).
func (s *Store) SetNow(f func() time.Time) { s.now = f }

func (s *Store) timeNow() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// Resolve maps a channel identity to its canonical identity key and
// reports whether any link exists. Unlinked identities resolve to
// themselves with linked=false: separate by default.
func (s *Store) Resolve(channel, userID string) (canonical string, linked bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := Key(channel, userID)
	if c, ok := s.links[key]; ok {
		return c, true
	}
	// A primary (others point at it) is its own canonical.
	for _, c := range s.links {
		if c == key {
			return key, true
		}
	}
	return key, false
}

// NewCode creates a one-time link code for a channel identity.
func (s *Store) NewCode(channel, userID string) (string, error) {
	var rnd [4]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", err
	}
	code := hex.EncodeToString(rnd[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneCodesLocked()
	s.codes = append(s.codes, linkCode{
		Code:      code,
		Owner:     Key(channel, userID),
		ExpiresAt: s.timeNow().Add(codeTTL),
	})
	if err := s.saveLocked(); err != nil {
		return "", err
	}
	return code, nil
}

// Redeem consumes a one-time code: the redeeming identity links to
// the code owner's canonical identity. Returns the canonical key.
func (s *Store) Redeem(channel, userID, code string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneCodesLocked()
	me := Key(channel, userID)
	for i, c := range s.codes {
		if c.Code != code {
			continue
		}
		// Consume first: a code fires exactly once, even if the
		// save below fails.
		s.codes = append(s.codes[:i], s.codes[i+1:]...)
		canonical := c.Owner
		if c.Owner == me {
			if err := s.saveLocked(); err != nil {
				return "", err
			}
			return me, errors.New("identity: a code links two DIFFERENT channels; you used it on the one that created it")
		}
		// A code from an already-linked identity points at its root.
		if root, ok := s.links[c.Owner]; ok {
			canonical = root
		}
		s.links[me] = canonical
		if err := s.saveLocked(); err != nil {
			return "", err
		}
		return canonical, nil
	}
	return "", errors.New("identity: unknown or expired code")
}

// Unlink dissolves one identity's link (and any links pointing at
// it). Returns whether anything was linked.
func (s *Store) Unlink(channel, userID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := Key(channel, userID)
	changed := false
	if _, ok := s.links[key]; ok {
		delete(s.links, key)
		changed = true
	}
	for k, c := range s.links {
		if c == key {
			delete(s.links, k)
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	return true, s.saveLocked()
}

// Linked lists the identity keys sharing the caller's canonical
// identity, for audit. Empty when unlinked.
func (s *Store) Linked(channel, userID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := Key(channel, userID)
	canonical, ok := s.links[key]
	if !ok {
		// Maybe the caller IS the primary.
		for _, c := range s.links {
			if c == key {
				canonical = key
				break
			}
		}
	}
	if canonical == "" {
		return nil
	}
	out := []string{canonical}
	for k, c := range s.links {
		if c == canonical && k != canonical {
			out = append(out, k)
		}
	}
	return out
}

// pruneCodesLocked drops expired codes.
func (s *Store) pruneCodesLocked() {
	now := s.timeNow()
	kept := s.codes[:0]
	for _, c := range s.codes {
		if c.ExpiresAt.After(now) {
			kept = append(kept, c)
		}
	}
	s.codes = kept
}

// saveLocked writes the identity file atomically (temp + rename).
func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(storeFile{Links: s.links, Codes: s.codes}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
