package evals

// Stage 7g battery case: the rolling session digest. A conversation
// long enough to force context-pruning compaction leaves a digest
// bullet in the sender's own digests.md (the test fails if it never
// lands on disk), a later query recalls it as data, and another
// sender never sees it.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/agent"
	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/memory"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

func TestSessionDigestWrittenAndRecalled(t *testing.T) {
	const marker = "el usuario planea un viaje a Lisboa en octubre"
	var lastBody atomic.Value // []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		lastBody.Store(body)
		w.Header().Set("Content-Type", "application/json")
		var req struct {
			Messages []model.Message `json:"messages"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			io.WriteString(w, reply("entendido"))
			return
		}
		for _, m := range req.Messages {
			// The pruner's summarizer pass (stage 15) answers with the
			// marker summary; every other call is a normal turn.
			if m.Role == "system" && strings.Contains(m.Content, "compresor de conversaciones") {
				io.WriteString(w, reply("RESUMEN: "+marker))
				return
			}
		}
		io.WriteString(w, reply("entendido"))
	}))
	t.Cleanup(srv.Close)

	kn, err := memory.OpenKnowledge(filepath.Join(t.TempDir(), "memory"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := memory.OpenJSON(filepath.Join(t.TempDir(), "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Model: config.Model{BaseURL: srv.URL, Name: "eval"}}
	a := agent.New(model.NewOpenAICompat(cfg.Model), store, tools.NewRegistry(), agent.SystemPrompt(cfg)).
		WithKnowledge(kn).
		WithPruning(agent.PruneConfig{MaxChars: 4000})

	ctx := context.Background()
	// 24 verbose turns (~1KB each) far exceed the 4000-char budget, so
	// the middle is compacted through the summarizer.
	for turn := 1; turn <= 24; turn++ {
		text := "turno de charla sobre nada en particular " + strings.Repeat("relleno ", 100)
		if _, err := a.Handle(ctx, "whatsapp", "u1", text); err != nil {
			t.Fatalf("turn %d: %v", turn, err)
		}
	}

	// The digest must be on disk, in the SENDER's scope.
	raw, err := os.ReadFile(filepath.Join(kn.Dir(), "users", "u1", "digests.md"))
	if err != nil {
		t.Fatalf("no digests.md in the sender scope: %v", err)
	}
	if !strings.Contains(string(raw), "Session digest") || !strings.Contains(string(raw), marker) {
		t.Fatalf("digest bullet missing or without the summary:\n%s", raw)
	}

	// A later query matching the digest terms recalls it as data.
	if _, err := a.Handle(ctx, "whatsapp", "u1", "qué sabes de mi viaje a Lisboa"); err != nil {
		t.Fatal(err)
	}
	body := lastBody.Load().([]byte)
	if !strings.Contains(string(body), "[digests]") || !strings.Contains(string(body), marker) {
		t.Fatalf("the digest was not recalled on the Lisboa turn: %s", body)
	}

	// Another sender asking the same thing sees no digest: scopes are
	// isolated by construction.
	if _, err := a.Handle(ctx, "whatsapp", "u2", "qué sabes de mi viaje a Lisboa"); err != nil {
		t.Fatal(err)
	}
	body = lastBody.Load().([]byte)
	if strings.Contains(string(body), "[digests]") || strings.Contains(string(body), marker) {
		t.Fatal("one sender's digest leaked into another sender's turn")
	}
}
