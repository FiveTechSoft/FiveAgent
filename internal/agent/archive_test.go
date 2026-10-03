package agent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

// TestIdlePassArchivesStaleEntries: stage 7l aging rides the idle
// consolidation pass - configured days move a stale stamped entry to
// archive/, recall loses it, and the recent entry stays.
func TestIdlePassArchivesStaleEntries(t *testing.T) {
	kn := openTestKnowledge(t)
	old := time.Now().AddDate(0, 0, -90).Format("2006-01-02")
	fresh := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	raw := "---\nid: preferences\n---\n# preferences\n" +
		"- Come pulpo a la gallega los domingos [" + old + ", origin: save_memory]\n" +
		"- Prefiere el verde musgo [" + fresh + ", origin: save_memory]\n"
	if err := os.WriteFile(filepath.Join(kn.Dir(), "preferences.md"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer srv.Close()

	cfg := &config.Config{Model: config.Model{BaseURL: srv.URL, Name: "m"}}
	a := New(model.NewOpenAICompat(cfg.Model), &fakeStore{}, tools.NewRegistry(), SystemPrompt(cfg))
	a.WithKnowledge(kn)
	a.WithArchiveDays(30)
	a.WithConsolidation(60 * time.Millisecond)

	// One turn sets the activity clock; the pass must fire alone after
	// the idle window, never inside the turn.
	if _, err := a.Handle(context.Background(), "whatsapp", "u1", "hola"); err != nil {
		t.Fatal(err)
	}

	archPath := filepath.Join(kn.Dir(), "archive", "preferences.md")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(archPath); err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	arch, err := os.ReadFile(archPath)
	if err != nil {
		t.Fatalf("idle pass never archived the stale entry: %v", err)
	}
	if !strings.Contains(string(arch), "pulpo") {
		t.Errorf("stale entry not in archive:\n%s", arch)
	}

	src, err := os.ReadFile(filepath.Join(kn.Dir(), "preferences.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "pulpo") {
		t.Errorf("stale entry still in preferences.md:\n%s", src)
	}
	if !strings.Contains(string(src), "musgo") {
		t.Errorf("the recent entry was lost:\n%s", src)
	}

	if hits, err := kn.Recall("pulpo gallega"); err == nil && len(hits) != 0 {
		t.Errorf("aged fact still recalled: %v", hits)
	}
	if hits, _ := kn.Recall("verde musgo"); len(hits) == 0 {
		t.Error("fresh fact no longer recalled")
	}
}

// TestArchiveDaysZeroLeavesFilesAlone: aging is opt-in; with the
// default (0) the idle pass never moves anything.
func TestArchiveDaysZeroLeavesFilesAlone(t *testing.T) {
	kn := openTestKnowledge(t)
	old := time.Now().AddDate(0, 0, -90).Format("2006-01-02")
	raw := "---\nid: preferences\n---\n# preferences\n" +
		"- Come pulpo a la gallega los domingos [" + old + ", origin: save_memory]\n"
	if err := os.WriteFile(filepath.Join(kn.Dir(), "preferences.md"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer srv.Close()

	cfg := &config.Config{Model: config.Model{BaseURL: srv.URL, Name: "m"}}
	a := New(model.NewOpenAICompat(cfg.Model), &fakeStore{}, tools.NewRegistry(), SystemPrompt(cfg))
	a.WithKnowledge(kn)
	a.WithConsolidation(60 * time.Millisecond)

	if _, err := a.Handle(context.Background(), "whatsapp", "u1", "hola"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(kn.Dir(), "archive")); err == nil {
		t.Error("archive/ created although archive_days is 0")
	}
}
