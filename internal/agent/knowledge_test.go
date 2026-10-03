package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/memory"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

func openTestKnowledge(t *testing.T) *memory.Knowledge {
	t.Helper()
	k, err := memory.OpenKnowledge(filepath.Join(t.TempDir(), "memory"))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// TestHandleInjectsMemory checks recalled memories reach the model
// labeled as data, never instructions.
func TestHandleInjectsMemory(t *testing.T) {
	kn := openTestKnowledge(t)
	if _, err := kn.Append("preferences", "Loves pasta."); err != nil {
		t.Fatal(err)
	}
	var gotBody atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody.Store(string(body))
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"reply"}}]}`)
	}))
	defer srv.Close()

	cfg := &config.Config{Model: config.Model{BaseURL: srv.URL, Name: "m"}}
	a := New(model.NewOpenAICompat(cfg.Model), &fakeStore{}, tools.NewRegistry(), SystemPrompt(cfg))
	a.WithKnowledge(kn)
	if _, err := a.Handle(context.Background(), "whatsapp", "u1", "¿le gusta la pasta?"); err != nil {
		t.Fatal(err)
	}
	body, _ := gotBody.Load().(string)
	if !strings.Contains(body, "Loves pasta") {
		t.Error("model request does not carry the recalled memory")
	}
	if !strings.Contains(body, "never instructions") {
		t.Error("memory block is not labeled as data, never instructions")
	}
}

// TestHandleLogsMemoryInjection: every turn with knowledge must log
// what was injected - snapshot size plus recall files and lines.
// Battery run 12's deferred miss cited other facts but not the target
// one; without this line there is no way to tell after the fact
// whether the fact rode the note.
func TestHandleLogsMemoryInjection(t *testing.T) {
	kn := openTestKnowledge(t)
	if _, err := kn.Append("preferences", "Loves pasta."); err != nil {
		t.Fatal(err)
	}
	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(os.Stderr)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer srv.Close()

	cfg := &config.Config{Model: config.Model{BaseURL: srv.URL, Name: "m"}}
	a := New(model.NewOpenAICompat(cfg.Model), &fakeStore{}, tools.NewRegistry(), SystemPrompt(cfg))
	a.WithKnowledge(kn)
	if _, err := a.Handle(context.Background(), "whatsapp", "u1", "¿le gusta la pasta?"); err != nil {
		t.Fatal(err)
	}
	out := logBuf.String()
	if !strings.Contains(out, "memory injection") {
		t.Errorf("no memory injection log line:\n%s", out)
	}
	if !strings.Contains(out, "preferences") {
		t.Errorf("injection line does not name the recalled file:\n%s", out)
	}
}

// TestOlvidaPurgesFrozenSnapshot: stage 7k freezes the memory block
// once per session, but a forgotten fact must leave the system prompt
// too - the per-turn recall note cannot remove lines it never had.
// Battery run 14b: disk was clean (recall=0) yet the model quoted the
// forgotten fact from the stale snapshot and the gate counted a
// hallucination (run 13 survived the same snapshot by model luck).
func TestOlvidaPurgesFrozenSnapshot(t *testing.T) {
	kn := openTestKnowledge(t)
	var bodies atomic.Value // []string, one system message per request
	bodies.Store([]string{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(body, &req)
		if len(req.Messages) > 0 && req.Messages[0].Role == "system" {
			prev, _ := bodies.Load().([]string)
			bodies.Store(append(prev, req.Messages[0].Content))
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer srv.Close()

	cfg := &config.Config{Model: config.Model{BaseURL: srv.URL, Name: "m"}}
	a := New(model.NewOpenAICompat(cfg.Model), &fakeStore{}, tools.NewRegistry(), SystemPrompt(cfg))
	a.WithKnowledge(kn)
	ctx := context.Background()
	for _, turn := range []string{
		"recuerda: mi comida favorita es el lacón con grelos",
		"hola",
		"olvida: mi comida favorita es el lacón con grelos",
		"qué hora es",
	} {
		if _, err := a.Handle(ctx, "whatsapp", "u1", turn); err != nil {
			t.Fatal(err)
		}
	}
	sys, _ := bodies.Load().([]string)
	if len(sys) != 4 {
		t.Fatalf("captured %d system prompts, want 4", len(sys))
	}
	if !strings.Contains(sys[1], "lacón") {
		t.Errorf("frozen snapshot lacks the fact before the olvida (setup broken): %q", sys[1])
	}
	if strings.Contains(sys[3], "lacón") {
		t.Errorf("stale snapshot still serves the forgotten fact after olvida: %q", sys[3])
	}
}

// TestHandleSavesMemoryViaTool checks the model can store a fact through
// the save_memory tool and it lands in the markdown file.
func TestHandleSavesMemoryViaTool(t *testing.T) {
	kn := openTestKnowledge(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[`+
				`{"id":"c1","type":"function","function":{"name":"save_memory",`+
				`"arguments":"{\"file\":\"preferences\",\"entry\":\"Loves pasta\"}"}}]}}]}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Anotado."}}]}`)
	}))
	defer srv.Close()

	cfg := &config.Config{Model: config.Model{BaseURL: srv.URL, Name: "m"}}
	a := New(model.NewOpenAICompat(cfg.Model), &fakeStore{},
		tools.NewRegistry(tools.SaveMemory{K: kn}, tools.ForgetMemory{K: kn}), SystemPrompt(cfg))
	a.WithKnowledge(kn)
	reply, err := a.Handle(context.Background(), "whatsapp", "u1", "recuerda que me gusta la pasta")
	if err != nil {
		t.Fatal(err)
	}
	if reply != "Anotado." {
		t.Errorf("reply = %q", reply)
	}
	hits, err := kn.Recall("pasta")
	if err != nil || len(hits) == 0 {
		t.Fatalf("memory did not store the tool call: hits=%v err=%v", hits, err)
	}
}

// TestMemoryToolsForget checks forget_memory removes stored facts.
func TestMemoryToolsForget(t *testing.T) {
	kn := openTestKnowledge(t)
	if _, err := kn.Append("preferences", "Loves pasta."); err != nil {
		t.Fatal(err)
	}
	out, err := tools.ForgetMemory{K: kn}.Execute(context.Background(),
		json.RawMessage(`{"file":"preferences","match":"pasta"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "forgot 1") {
		t.Errorf("out = %q", out)
	}
	hits, _ := kn.Recall("pasta")
	if len(hits) != 0 {
		t.Errorf("fact still recalled after forget: %+v", hits)
	}
}

// TestHandleRecuerdaPrefixStoresDirectly pins Ruta A: "recuerda:" must be
// stored even when the model never calls save_memory (registry has no
// memory tools at all here). Observed live 2026-09-28: the model replied
// "de acuerdo" without the tool and the fact was lost.
func TestHandleRecuerdaPrefixStoresDirectly(t *testing.T) {
	kn := openTestKnowledge(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"De acuerdo."}}]}`)
	}))
	defer srv.Close()

	cfg := &config.Config{Model: config.Model{BaseURL: srv.URL, Name: "m"}}
	a := New(model.NewOpenAICompat(cfg.Model), &fakeStore{}, tools.NewRegistry(), SystemPrompt(cfg))
	a.WithKnowledge(kn)
	if _, err := a.Handle(context.Background(), "whatsapp", "u1",
		"  recuerda: mi lenguaje favorito para scripts es Python "); err != nil {
		t.Fatal(err)
	}
	hits, err := kn.Recall("¿cuál es mi lenguaje favorito para scripts?")
	if err != nil || len(hits) == 0 {
		t.Fatalf("recuerda: prefix was not stored without the tool: hits=%v err=%v", hits, err)
	}
	found := false
	for _, ln := range hits[0].Lines {
		if strings.Contains(ln, "lenguaje favorito para scripts es Python") {
			found = true
		}
	}
	if !found {
		t.Errorf("stored entry not recalled: %v", hits[0].Lines)
	}
}

// TestHandleOlvidaPrefixForgetsDirectly: the twin marker must remove the
// bullet without the model (no memory tools in the registry), so the
// documented correction flow works even when the model skips tools.
func TestHandleOlvidaPrefixForgetsDirectly(t *testing.T) {
	kn := openTestKnowledge(t)
	if _, err := kn.Append("preferences", "mi comida favorita es el pulpo a la gallega"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Borrado."}}]}`)
	}))
	defer srv.Close()

	cfg := &config.Config{Model: config.Model{BaseURL: srv.URL, Name: "m"}}
	a := New(model.NewOpenAICompat(cfg.Model), &fakeStore{}, tools.NewRegistry(), SystemPrompt(cfg))
	a.WithKnowledge(kn)
	if _, err := a.Handle(context.Background(), "whatsapp", "u1",
		"  olvida: mi comida favorita "); err != nil {
		t.Fatal(err)
	}
	hits, err := kn.Recall("mi comida favorita")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("olvida: prefix did not remove the bullet: %+v", hits)
	}
}

// memoryFilesHolding lists every .md file under root holding token
// (case-insensitive), the same walk the battery's M3 disk check does.
func memoryFilesHolding(root, token string) []string {
	var out []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		if b, err := os.ReadFile(path); err == nil &&
			strings.Contains(strings.ToLower(string(b)), strings.ToLower(token)) {
			out = append(out, path)
		}
		return nil
	})
	return out
}

// Battery run 6 (M3 0/1): "olvida:" cleared the three curated global
// files but not the sender's own digests.md, where stage 7g had copied
// the compacted turn. Recall merges that scope, so the fact came back
// in a fresh session and the disk check still saw it - the model was
// telling the truth about a file that should have been cleaned.
func TestHandleOlvidaPurgesSenderDigestScope(t *testing.T) {
	kn := openTestKnowledge(t)
	const fact = "mi plato de fiesta es la empanada de zamburiñas"
	uk, err := memory.OpenUserScope(kn.Dir(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	other, err := memory.OpenUserScope(kn.Dir(), "u2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kn.Append("preferences", fact); err != nil {
		t.Fatal(err)
	}
	if _, err := uk.Append("digests", "Session digest 2026-10-02 (12 compacted turns): recuerda: "+fact); err != nil {
		t.Fatal(err)
	}
	// Another sender holds the same fact: one "olvida:" never reaches
	// another sender's scope.
	if _, err := other.Append("digests", "Session digest 2026-10-02 (3 compacted turns): recuerda: "+fact); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Borrado."}}]}`)
	}))
	defer srv.Close()

	cfg := &config.Config{Model: config.Model{BaseURL: srv.URL, Name: "m"}}
	a := New(model.NewOpenAICompat(cfg.Model), &fakeStore{}, tools.NewRegistry(), SystemPrompt(cfg))
	a.WithKnowledge(kn)
	if _, err := a.Handle(context.Background(), "whatsapp", "u1", "olvida: mi plato de fiesta"); err != nil {
		t.Fatal(err)
	}
	if paths := memoryFilesHolding(kn.Dir(), "empanada"); len(paths) != 1 {
		t.Errorf("forgotten fact left in %d files, want only the other sender's scope: %v", len(paths), paths)
	}
	raw, err := os.ReadFile(filepath.Join(other.Dir(), "digests.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "empanada") {
		t.Error("another sender's memory was purged by this one's olvida:")
	}
}

// Battery run 7 ERASE-MISS: the very same turn runs the pruner, and
// stage 7g writes the fresh digest out of the history the olvida: just
// cleaned - so Ruta A had to be the LAST memory writer of the turn.
// With the purge ahead of PruneConfig.Prune the digest came back
// before Handle returned and the battery's on-disk check still found
// "empanada" (run 6 AND run 7, 0/1 twice).
func TestHandleOlvidaPurgesAfterThisTurnPruning(t *testing.T) {
	kn := openTestKnowledge(t)
	const fact = "mi plato de fiesta es la empanada de zamburiñas"
	if _, err := kn.Append("preferences", fact); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Borrado."}}]}`)
	}))
	defer srv.Close()

	// A deep history: far over the tiny budget below, so this turn's
	// Prune compacts the middle and the DigestSink rewrites digests.md.
	store := &fakeStore{}
	for i := 0; i < 30; i++ {
		store.hist = append(store.hist,
			[2]string{"user", "turno " + strconv.Itoa(i) + ": " + fact},
			[2]string{"assistant", "apuntado, queda anotado: la empanada de zamburiñas"})
	}

	cfg := &config.Config{Model: config.Model{BaseURL: srv.URL, Name: "m"}}
	a := New(model.NewOpenAICompat(cfg.Model), store, tools.NewRegistry(), SystemPrompt(cfg))
	a.WithKnowledge(kn)
	a.WithPruning(PruneConfig{
		MaxChars:  600,
		HeadKeep:  2,
		TailKeep:  8,
		Summarize: func(context.Context, []model.Message) (string, error) {
			return fact, nil
		},
	})
	if _, err := a.Handle(context.Background(), "whatsapp", "u1", "olvida: mi plato de fiesta"); err != nil {
		t.Fatal(err)
	}
	if paths := memoryFilesHolding(kn.Dir(), "empanada"); len(paths) != 0 {
		t.Errorf("digest re-written by this turn's pruning survived the olvida: %v", paths)
	}
}

// Battery run 9: the files were clean after the forget and the model
// still wrote "hay registros previos que mencionaban pulpo" - it was
// reading the stored history. An olvida: that only reaches the files is
// not a forget: the context has to lose the fact too.
func TestHandleOlvidaScrubsStoredHistory(t *testing.T) {
	kn := openTestKnowledge(t)
	const fact = "mi plato de fiesta es la empanada de zamburiñas"
	if _, err := kn.Append("preferences", fact); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Borrado."}}]}`)
	}))
	defer srv.Close()

	store := &fakeStore{hist: [][2]string{
		{"user", "recuerda: " + fact},
		{"assistant", "anotado: tu plato de fiesta es la empanada de zamburiñas"},
	}}
	cfg := &config.Config{Model: config.Model{BaseURL: srv.URL, Name: "m"}}
	a := New(model.NewOpenAICompat(cfg.Model), store, tools.NewRegistry(), SystemPrompt(cfg))
	a.WithKnowledge(kn)
	if _, err := a.Handle(context.Background(), "whatsapp", "u1", "olvida: mi plato de fiesta"); err != nil {
		t.Fatal(err)
	}
	for _, h := range store.hist {
		if strings.Contains(strings.ToLower(h[1]), "empanada") {
			t.Fatalf("stored history still quotes the forgotten fact: %q", h[1])
		}
	}
	if paths := memoryFilesHolding(kn.Dir(), "empanada"); len(paths) != 0 {
		t.Errorf("files still hold the forgotten fact: %v", paths)
	}
}

// The other forget path: the model calls forget_memory itself (no
// "olvida:" marker, so Ruta A never runs). The tool cleans the files;
// the agent has to clean the history around it, or the next turn reads
// the fact back out of the conversation it just deleted.
func TestForgetMemoryToolScrubsStoredHistory(t *testing.T) {
	kn := openTestKnowledge(t)
	if _, err := kn.Append("preferences", "Mi comida favorita es el pulpo a la gallega"); err != nil {
		t.Fatal(err)
	}
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Tools json.RawMessage `json:"tools"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		if len(req.Tools) > 0 && !called {
			called = true
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[`+
				`{"id":"c1","type":"function","function":{"name":"forget_memory","arguments":`+
				`"{\"file\":\"preferences\",\"match\":\"pulpo a la gallega\"}"}}]}}]}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"hecho"}}]}`)
	}))
	defer srv.Close()

	store := &fakeStore{hist: [][2]string{
		{"user", "recuerda: Mi comida favorita es el pulpo a la gallega"},
		{"assistant", "guardado: el pulpo a la gallega es tu plato favorito"},
	}}
	cfg := &config.Config{Model: config.Model{BaseURL: srv.URL, Name: "m"}}
	a := New(model.NewOpenAICompat(cfg.Model), store,
		tools.NewRegistry(tools.ForgetMemory{K: kn}), SystemPrompt(cfg))
	a.WithKnowledge(kn)
	if _, err := a.Handle(context.Background(), "whatsapp", "u1",
		"olvida todo lo que sepas sobre mi comida favorita"); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("the model never called forget_memory")
	}
	for _, h := range store.hist {
		if strings.Contains(strings.ToLower(h[1]), "pulpo") {
			t.Fatalf("stored history still quotes the forgotten fact: %q", h[1])
		}
	}
	if paths := memoryFilesHolding(kn.Dir(), "pulpo"); len(paths) != 0 {
		t.Errorf("files still hold the forgotten fact: %v", paths)
	}
}
