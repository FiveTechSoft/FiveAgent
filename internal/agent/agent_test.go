package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"

	"github.com/FiveTechSoft/FiveAgent/internal/config"
)

func TestSystemPromptDefaultNamesModel(t *testing.T) {
	cfg := &config.Config{
		Model: config.Model{
			BaseURL: "https://api.deepseek.com/v1",
			Name:    "deepseek-chat",
		},
	}
	p := SystemPrompt(cfg)
	if !strings.Contains(p, baseSystemPrompt) {
		t.Errorf("default persona missing: %q", p)
	}
	if !strings.Contains(p, "deepseek-chat") {
		t.Errorf("model name missing: %q", p)
	}
	if !strings.Contains(p, "api.deepseek.com") {
		t.Errorf("endpoint host missing: %q", p)
	}
	if strings.Contains(p, "api.deepseek.com/v1") {
		t.Errorf("host should not include the path: %q", p)
	}
}

func TestSystemPromptCustomKeepsIdentityLine(t *testing.T) {
	cfg := &config.Config{
		SystemPrompt: "You are Paco, a grumpy cat.",
		Model: config.Model{
			BaseURL: "http://localhost:11434/v1",
			Name:    "qwen3",
		},
	}
	p := SystemPrompt(cfg)
	if !strings.HasPrefix(p, "You are Paco, a grumpy cat.") {
		t.Errorf("custom prompt missing: %q", p)
	}
	if strings.Contains(p, "FiveAgent, a helpful") {
		t.Errorf("default persona should be replaced: %q", p)
	}
	if !strings.Contains(p, "qwen3") || !strings.Contains(p, "localhost:11434") {
		t.Errorf("identity line missing: %q", p)
	}
}

func TestSystemPromptWhitespaceOnlyFallsBack(t *testing.T) {
	cfg := &config.Config{
		SystemPrompt: "   ",
		Model:        config.Model{BaseURL: "http://localhost:11434/v1", Name: "qwen3"},
	}
	if p := SystemPrompt(cfg); !strings.Contains(p, baseSystemPrompt) {
		t.Errorf("whitespace-only prompt should fall back to default: %q", p)
	}
}

func TestSystemPromptMentionsProviderSwitch(t *testing.T) {
	cfg := &config.Config{
		Model: config.Model{BaseURL: "https://api.deepseek.com/v1", Name: "deepseek-chat"},
	}
	p := SystemPrompt(cfg)
	for _, want := range []string{"Ollama", "DeepSeek", "fiveagent.yml"} {
		if !strings.Contains(p, want) {
			t.Errorf("identity line should mention %q: %q", want, p)
		}
	}
}

func TestChannelStyle(t *testing.T) {
	w := channelStyle("whatsapp")
	if !strings.Contains(w, "*bold*") || !strings.Contains(w, "_italic_") {
		t.Errorf("whatsapp style should document WhatsApp markup: %q", w)
	}
	if !strings.Contains(w, "no Markdown") {
		t.Errorf("whatsapp style should forbid full Markdown: %q", w)
	}
	tg := channelStyle("telegram")
	if !strings.Contains(tg, "plain text") {
		t.Errorf("telegram style should require plain text: %q", tg)
	}
	if got := channelStyle("imessage"); !strings.Contains(got, "plain text") {
		t.Errorf("unknown channels should fall back to plain text: %q", got)
	}
}

func TestSystemPromptStatesOpenSource(t *testing.T) {
	cfg := &config.Config{
		Model: config.Model{BaseURL: "https://api.deepseek.com/v1", Name: "deepseek-chat"},
	}
	p := SystemPrompt(cfg)
	for _, want := range []string{"open source", "MIT", "https://github.com/FiveTechSoft/FiveAgent"} {
		if !strings.Contains(p, want) {
			t.Errorf("identity line should state %q: %q", want, p)
		}
	}
}

type fakeStore struct {
	hist [][2]string
}

func (f *fakeStore) Append(_ context.Context, _, _, role, content string) error {
	f.hist = append(f.hist, [2]string{role, content})
	return nil
}

func (f *fakeStore) Recent(_ context.Context, _, _ string, _ int) ([][2]string, error) {
	return f.hist, nil
}

func (f *fakeStore) Close() error { return nil }

func TestHandleIgnoresStoredSystemPrompt(t *testing.T) {
	var got []model.Message
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []model.Message `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		got = req.Messages
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"hola"}}]}`)
	}))
	defer srv.Close()

	cfg := &config.Config{Model: config.Model{BaseURL: srv.URL, Name: "test-model"}}
	mdl := model.NewOpenAICompat(cfg.Model)
	store := &fakeStore{hist: [][2]string{
		{"system", "OLD STALE PROMPT"},
		{"user", "hola"},
	}}
	a := New(mdl, store, tools.NewRegistry(), SystemPrompt(cfg))

	if _, err := a.Handle(context.Background(), "whatsapp", "u1", "hola"); err != nil {
		t.Fatal(err)
	}
	var sys []string
	for _, m := range got {
		if m.Role == "system" {
			sys = append(sys, m.Content)
		}
		if strings.Contains(m.Content, "OLD STALE PROMPT") {
			t.Fatalf("stale system prompt reached the model: %+v", got)
		}
	}
	if len(sys) != 1 {
		t.Fatalf("expected exactly 1 system message, got %d: %v", len(sys), sys)
	}
	if !strings.HasPrefix(sys[0], baseSystemPrompt) {
		t.Fatalf("system message is not the current prompt: %q", sys[0])
	}
}

func TestDomainReferenceRidesTheSkillNotThePrompt(t *testing.T) {
	cfg := &config.Config{}
	cfg.Model.Name = "qwen3"
	cfg.Model.BaseURL = "http://localhost:11434/v1"
	p := SystemPrompt(cfg)
	// Stage 17: the base prompt no longer pays for the domain
	// reference on every turn.
	if strings.Contains(p, "FiveWin for Harbour") || strings.Contains(p, "AUTHORITATIVE") {
		t.Error("system prompt must NOT carry the domain reference unconditionally (stage 17)")
	}
	block := DomainSkill().Load()
	if !strings.Contains(block, "FiveWin for Harbour") {
		t.Error("the domain skill must carry the embedded FiveTech domain reference")
	}
	if !strings.Contains(block, "AUTHORITATIVE") {
		t.Error("the domain skill must mark the reference as overriding general knowledge")
	}
}

func TestSkillMatches(t *testing.T) {
	sk := DomainSkill()
	cases := []struct {
		text string
		want bool
	}{
		{"escríbeme un programa en Harbour que pida mi nombre", true},
		{"¿qué es FWH?", true},
		{"¿cómo se crea una ventana en FiveWin?", true},
		{"migración de xHarbour a Harbour", true},
		{"¿cuál es la capital de Portugal?", false},
		{"fwhello no es un disparador", false},
		{"", false},
	}
	for _, c := range cases {
		if got := skillMatches(sk, c.text); got != c.want {
			t.Errorf("skillMatches(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

func TestDomainPrefersRuntimeFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "fivetech-domain.md"), []byte("MARKER-DOMAIN-RUNTIME"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	cfg := &config.Config{}
	cfg.Model.Name = "m"
	cfg.Model.BaseURL = "http://x"
	if p := DomainSkill().Load(); !strings.Contains(p, "MARKER-DOMAIN-RUNTIME") {
		t.Error("the runtime file must win over the embedded copy")
	}
}

func TestDomainFallsBackToEmbedded(t *testing.T) {
	t.Chdir(t.TempDir()) // no docs/ here
	cfg := &config.Config{}
	cfg.Model.Name = "m"
	cfg.Model.BaseURL = "http://x"
	if p := DomainSkill().Load(); !strings.Contains(p, "FiveWin for Harbour") {
		t.Error("a missing runtime file must fall back to the embedded copy")
	}
}

func TestHandleForcesFinalAnswerAfterToolRounds(t *testing.T) {
	var toolRounds, finalRounds int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Tools json.RawMessage `json:"tools"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		if len(req.Tools) > 0 {
			toolRounds++
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[`+
				`{"id":"c1","type":"function","function":{"name":"current_datetime","arguments":"{}"}}]}}]}`)
			return
		}
		finalRounds++
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"respuesta final"}}]}`)
	}))
	defer srv.Close()

	cfg := &config.Config{Model: config.Model{BaseURL: srv.URL, Name: "test-model"}}
	mdl := model.NewOpenAICompat(cfg.Model)
	a := New(mdl, &fakeStore{}, tools.NewRegistry(tools.Datetime{}), SystemPrompt(cfg))

	reply, err := a.Handle(context.Background(), "whatsapp", "u1", "hola")
	if err != nil {
		t.Fatalf("a model that only tool-calls must still get an answer, not an error: %v", err)
	}
	if reply != "respuesta final" {
		t.Errorf("reply = %q, want %q", reply, "respuesta final")
	}
	if toolRounds != maxToolRounds {
		t.Errorf("tool rounds = %d, want %d", toolRounds, maxToolRounds)
	}
	if finalRounds != 1 {
		t.Errorf("forced no-tools calls = %d, want exactly 1", finalRounds)
	}
}

// TestHandleRetriesEmptyForcedAnswer covers the observed qwen3.5 flake:
// with no tools it can answer with empty content + finish=stop. The agent
// must retry the forced call instead of giving up.
func TestHandleRetriesEmptyForcedAnswer(t *testing.T) {
	var finalRounds int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Tools json.RawMessage `json:"tools"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		if len(req.Tools) > 0 {
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[`+
				`{"id":"c1","type":"function","function":{"name":"current_datetime","arguments":"{}"}}]}}]}`)
			return
		}
		finalRounds++
		if finalRounds == 1 {
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":""}}]}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"respuesta final"}}]}`)
	}))
	defer srv.Close()

	cfg := &config.Config{Model: config.Model{BaseURL: srv.URL, Name: "test-model"}}
	a := New(model.NewOpenAICompat(cfg.Model), &fakeStore{}, tools.NewRegistry(tools.Datetime{}), SystemPrompt(cfg))

	reply, err := a.Handle(context.Background(), "whatsapp", "u1", "hola")
	if err != nil {
		t.Fatalf("an empty forced answer must be retried, not fail: %v", err)
	}
	if reply != "respuesta final" {
		t.Errorf("reply = %q, want %q", reply, "respuesta final")
	}
	if finalRounds != 2 {
		t.Errorf("forced no-tools calls = %d, want 2 (one empty + one retry)", finalRounds)
	}
}

// TestHandleFallsBackWhenFinalAnswerEmpty: every forced answer comes back
// empty. The turn must still end with non-empty words (model's own from a
// tool-call turn, else the fixed honest line) and no error.
func TestHandleFallsBackWhenFinalAnswerEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Tools json.RawMessage `json:"tools"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		if len(req.Tools) > 0 {
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"palabras del modelo","tool_calls":[`+
				`{"id":"c1","type":"function","function":{"name":"current_datetime","arguments":"{}"}}]}}]}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":""}}]}`)
	}))
	defer srv.Close()

	cfg := &config.Config{Model: config.Model{BaseURL: srv.URL, Name: "test-model"}}
	a := New(model.NewOpenAICompat(cfg.Model), &fakeStore{}, tools.NewRegistry(tools.Datetime{}), SystemPrompt(cfg))

	reply, err := a.Handle(context.Background(), "whatsapp", "u1", "hola")
	if err != nil {
		t.Fatalf("empty forced answers must degrade the reply, not error: %v", err)
	}
	if reply != "palabras del modelo" {
		t.Errorf("reply = %q, want the model's own words from the tool turn", reply)
	}
}
