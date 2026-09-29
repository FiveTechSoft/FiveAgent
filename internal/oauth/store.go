package oauth

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/FiveTechSoft/FiveAgent/internal/secrets"
)

// TokenStore keeps one token per integration name in a JSON file:
// 0600 permissions (POSIX; on Windows there are no POSIX bits and
// protection is the user profile ACL), atomic tmp+rename writes,
// never a second source of truth.
//
// Encryption at rest (stage 8): with a Cipher set, saves write a
// sealed AES-256-GCM envelope and loads open it. A plaintext store
// found on load is migrated without loss: it is parsed first, then
// re-saved sealed; if the re-save fails the tokens still load (the
// migration retries on the next save or load). Without a Cipher the
// store stays plaintext - main wires the cipher from config, so an
// unset key source means an explicit operator choice, not a silent
// default.
type TokenStore struct {
	Path   string
	Cipher *secrets.Cipher // nil keeps the store plaintext
}

func (s *TokenStore) read() (map[string]*Token, error) {
	raw, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, err
	}
	if secrets.IsSealed(raw) {
		if s.Cipher == nil {
			return nil, fmt.Errorf("token store %s is encrypted but no key is configured (set secrets.key_file or FIVEAGENT_MASTER_KEY)", s.Path)
		}
		raw, err = s.Cipher.Open(raw)
		if err != nil {
			return nil, fmt.Errorf("token store %s: %w", s.Path, err)
		}
	}
	plaintext := raw
	if secrets.IsSealed(raw) {
		plaintext = nil // already sealed, nothing to migrate
	}
	var m map[string]*Token
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("token store %s unreadable: %w", s.Path, err)
	}
	if plaintext != nil && s.Cipher != nil {
		// Plaintext store with a cipher configured: migrate without
		// loss - the parse above already succeeded, so the re-save
		// seals good data. A failed re-save never blocks the read;
		// the migration retries on the next load or save.
		if err := s.saveRaw(plaintext); err != nil {
			log.Printf("oauth: token store migration to encrypted save deferred: %v", err)
		}
	}
	return m, nil
}

// Save writes the token for name, creating the file 0600.
func (s *TokenStore) Save(name string, tok *Token) error {
	m, _ := s.read()
	if m == nil {
		m = map[string]*Token{}
	}
	m[name] = tok
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return s.saveRaw(raw)
}

// saveRaw writes the token map, sealing it when a cipher is set.
func (s *TokenStore) saveRaw(raw []byte) error {
	if s.Cipher != nil {
		sealed, err := s.Cipher.Seal(raw)
		if err != nil {
			return err
		}
		raw = sealed
	}
	tmp := s.Path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

// Load reads the token for name.
func (s *TokenStore) Load(name string) (*Token, error) {
	m, err := s.read()
	if err != nil {
		return nil, err
	}
	tok, ok := m[name]
	if !ok {
		return nil, fmt.Errorf("no token for %q", name)
	}
	return tok, nil
}
