package oauth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// TokenStore keeps one token per integration name in a JSON file:
// 0600 permissions (POSIX; on Windows there are no POSIX bits and
// protection is the user profile ACL), atomic tmp+rename writes,
// never a second source of truth. NOT encrypted at rest - roadmap
// stage 8 owns that.
type TokenStore struct {
	Path string
}

func (s *TokenStore) read() (map[string]*Token, error) {
	raw, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, err
	}
	var m map[string]*Token
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("token store %s unreadable: %w", s.Path, err)
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
