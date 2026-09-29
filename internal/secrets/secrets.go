// Package secrets is stage 8: authenticated encryption for stored
// credentials (AES-256-GCM), with an explicit, honest threat model.
//
// Threat model - what this protects and what it does not:
//   - An attacker who copies the token file (backup, sync client,
//     leaked archive) gets ciphertext. That is the real win.
//   - An attacker who reads the same live disk as the agent gets the
//     key too when it lives in a key file next to the data. A key
//     file on the same disk protects COPIES of the data file, not
//     the live machine. Saying otherwise would be smoke.
//   - The strongest source is the environment (FIVEAGENT_MASTER_KEY),
//     which keeps the key out of the filesystem entirely; the key
//     file exists so the feature works with zero operator setup.
//
// The envelope is self-describing JSON so plaintext stores are
// detected and migrated without loss, and a wrong key fails loudly
// (GCM authentication) instead of decrypting to garbage.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Envelope is the on-disk shape of a sealed blob.
type Envelope struct {
	Sealed string `json:"fiveagent_sealed"` // marker: "v1"
	Nonce  string `json:"nonce"`            // base64
	Data   string `json:"data"`             // base64 ciphertext
}

const envelopeVersion = "v1"

// Cipher seals and opens blobs with AES-256-GCM.
type Cipher struct {
	gcm cipher.AEAD
}

// NewCipher requires exactly 32 bytes of key.
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("secrets: key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{gcm: gcm}, nil
}

// GenerateKey returns 32 random bytes.
func GenerateKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

// ParseKey accepts base64 or hex (with optional whitespace).
func ParseKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("secrets: empty key")
	}
	if key, err := base64.StdEncoding.DecodeString(s); err == nil && len(key) == 32 {
		return key, nil
	}
	if key, err := hex.DecodeString(s); err == nil && len(key) == 32 {
		return key, nil
	}
	return nil, fmt.Errorf("secrets: key must be 32 bytes as base64 or hex")
}

// LoadOrCreateKeyFile reads a 0600 key file, generating one on first
// use. Documented limitation: a key file next to the data protects
// copies of the data file, not the live disk.
func LoadOrCreateKeyFile(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		return ParseKey(string(raw))
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	key, err := GenerateKey()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(base64.StdEncoding.EncodeToString(key)+"\n"), 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, err
	}
	return key, nil
}

// Seal encrypts plaintext into a self-describing JSON envelope.
func (c *Cipher) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	env := Envelope{
		Sealed: envelopeVersion,
		Nonce:  base64.StdEncoding.EncodeToString(nonce),
		Data:   base64.StdEncoding.EncodeToString(c.gcm.Seal(nil, nonce, plaintext, nil)),
	}
	return json.MarshalIndent(env, "", "  ")
}

// IsSealed reports whether data looks like an envelope (used to
// detect plaintext stores for migration).
func IsSealed(data []byte) bool {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return false
	}
	return env.Sealed == envelopeVersion && env.Nonce != "" && env.Data != ""
}

// Open decrypts an envelope. A wrong key or a tampered blob fails
// with GCM authentication - never garbage output.
func (c *Cipher) Open(data []byte) ([]byte, error) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("secrets: not an envelope: %w", err)
	}
	if env.Sealed != envelopeVersion {
		return nil, fmt.Errorf("secrets: unknown envelope version %q", env.Sealed)
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil {
		return nil, fmt.Errorf("secrets: bad nonce: %w", err)
	}
	raw, err := base64.StdEncoding.DecodeString(env.Data)
	if err != nil {
		return nil, fmt.Errorf("secrets: bad ciphertext: %w", err)
	}
	plain, err := c.gcm.Open(nil, nonce, raw, nil)
	if err != nil {
		return nil, fmt.Errorf("secrets: decryption failed (wrong key or tampered data): %w", err)
	}
	return plain, nil
}
