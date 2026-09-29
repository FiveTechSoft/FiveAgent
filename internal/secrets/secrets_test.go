package secrets

// Stage 8 battery: round-trip, wrong-key failure, tamper detection,
// key parsing, key-file creation (POSIX perms asserted only off
// Windows - no POSIX bits there), envelope detection.

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	key, _ := GenerateKey()
	c, err := NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	blob := []byte(`{"gmail":{"access_token":"secret-token"}}`)
	sealed, err := c.Seal(blob)
	if err != nil {
		t.Fatal(err)
	}
	if !IsSealed(sealed) {
		t.Fatal("sealed blob not detected as envelope")
	}
	if strings.Contains(string(sealed), "secret-token") {
		t.Fatal("ciphertext leaks the plaintext")
	}
	back, err := c.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if string(back) != string(blob) {
		t.Fatalf("round trip mismatch: %s", back)
	}
}

func TestWrongKeyFails(t *testing.T) {
	k1, _ := GenerateKey()
	k2, _ := GenerateKey()
	c1, _ := NewCipher(k1)
	c2, _ := NewCipher(k2)
	sealed, _ := c1.Seal([]byte("data"))
	if _, err := c2.Open(sealed); err == nil || !strings.Contains(err.Error(), "decryption failed") {
		t.Fatalf("wrong key must fail loudly: %v", err)
	}
}

func TestTamperFails(t *testing.T) {
	key, _ := GenerateKey()
	c, _ := NewCipher(key)
	sealed, _ := c.Seal([]byte("data"))
	// Flip one byte inside the base64 ciphertext region.
	i := strings.LastIndex(string(sealed), "A")
	tampered := []byte(string(sealed[:i]) + "B" + string(sealed[i+1:]))
	if _, err := c.Open(tampered); err == nil {
		t.Fatal("tampered blob must not open")
	}
}

func TestPlaintextIsNotSealed(t *testing.T) {
	if IsSealed([]byte(`{"gmail":{"access_token":"a"}}`)) {
		t.Fatal("plaintext token store misdetected as sealed")
	}
}

func TestParseKey(t *testing.T) {
	key, _ := GenerateKey()
	c, err := NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	for _, enc := range []string{
		base64Of(key),
		hexOf(key),
		"  " + base64Of(key) + "\n",
	} {
		got, err := ParseKey(enc)
		if err != nil {
			t.Fatalf("ParseKey(%q): %v", enc, err)
		}
		c2, err := NewCipher(got)
		if err != nil {
			t.Fatal(err)
		}
		sealed, _ := c.Seal([]byte("x"))
		if _, err := c2.Open(sealed); err != nil {
			t.Fatalf("parsed key does not match: %v", err)
		}
	}
	if _, err := ParseKey("short"); err == nil {
		t.Fatal("short key must be rejected")
	}
	if _, err := NewCipher([]byte("short")); err == nil {
		t.Fatal("NewCipher must require 32 bytes")
	}
}

func TestLoadOrCreateKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "master.key")
	key, err := LoadOrCreateKeyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 32 {
		t.Fatalf("key length: %d", len(key))
	}
	// Second load returns the SAME key (no rotation behind our back).
	again, err := LoadOrCreateKeyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(key) {
		t.Fatal("key file not stable across loads")
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("key file perms: %o", fi.Mode().Perm())
		}
	}
}

func base64Of(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	var sb strings.Builder
	for _, x := range b {
		sb.WriteByte(digits[x>>4])
		sb.WriteByte(digits[x&0xf])
	}
	return sb.String()
}
