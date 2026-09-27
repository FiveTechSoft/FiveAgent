package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenJSONStripsStoredSystemMessages(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mem.json")
	seed := `[
	  {"channel":"whatsapp","user_id":"u1","role":"system","content":"OLD PROMPT","created_at":"2026-09-27T10:00:00Z"},
	  {"channel":"whatsapp","user_id":"u1","role":"user","content":"hola","created_at":"2026-09-27T10:01:00Z"},
	  {"channel":"whatsapp","user_id":"u1","role":"assistant","content":"buenas","created_at":"2026-09-27T10:01:05Z"}
	]`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := OpenJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	hist, err := st.Recent(context.Background(), "whatsapp", "u1", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 {
		t.Fatalf("expected 2 messages after migration, got %d: %v", len(hist), hist)
	}
	for _, h := range hist {
		if h[0] == "system" {
			t.Fatalf("system message survived migration: %v", h)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "OLD PROMPT") {
		t.Fatal("migration did not rewrite the file")
	}
}
