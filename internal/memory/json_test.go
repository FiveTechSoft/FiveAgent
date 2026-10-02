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

// An olvida: has to reach the live context too: without Scrub the model
// quotes the forgotten fact straight out of history (battery run 9:
// the files were clean and it still answered with "pulpo").
func TestScrubRedactsStoredHistory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mem.json")
	st, err := OpenJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	mustAppend := func(channel, user, role, content string) {
		if err := st.Append(ctx, channel, user, role, content); err != nil {
			t.Fatal(err)
		}
	}
	mustAppend("whatsapp", "u1", "user", "recuerda: mi comida favorita es el pulpo a la gallega")
	mustAppend("whatsapp", "u1", "assistant", "anotado: el pulpo a la gallega queda guardado")
	mustAppend("whatsapp", "u2", "user", "a mi me gusta el pulpo a la gallega")

	n, err := st.Scrub(ctx, "whatsapp", "u1", []string{"pulpo", "gallega"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("scrub rewrote %d messages, want 2", n)
	}
	hist, err := st.Recent(ctx, "whatsapp", "u1", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hist {
		if strings.Contains(strings.ToLower(h[1]), "pulpo") {
			t.Fatalf("stored history still quotes the forgotten fact: %q", h[1])
		}
	}
	other, err := st.Recent(ctx, "whatsapp", "u2", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 1 || !strings.Contains(other[0][1], "pulpo") {
		t.Errorf("another user's history was scrubbed: %v", other)
	}
	// The redaction is on disk, not just in the loaded copy.
	reopened, err := OpenJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	again, err := reopened.Recent(ctx, "whatsapp", "u1", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range again {
		if strings.Contains(strings.ToLower(h[1]), "pulpo") {
			t.Fatalf("redaction did not survive a reopen: %q", h[1])
		}
	}
}
