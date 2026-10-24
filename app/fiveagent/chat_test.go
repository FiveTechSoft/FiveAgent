package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/model"
)

// fakeChatModel is a minimal OpenAI-compatible /v1/chat/completions
// stand-in. It records every request's messages and replies with
// scripted contents in call order (last script entry repeats).
type fakeChatModel struct {
	t       *testing.T
	mu      sync.Mutex
	script  []string
	request [][]model.Message
	srv     *httptest.Server
}

func newFakeChatModel(t *testing.T, script ...string) *fakeChatModel {
	t.Helper()
	f := &fakeChatModel{t: t, script: script}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []model.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		f.mu.Lock()
		f.request = append(f.request, req.Messages)
		i := len(f.request) - 1
		if i >= len(f.script) {
			i = len(f.script) - 1
		}
		reply := f.script[i]
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": reply}},
			},
		})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeChatModel) lastRequest() []model.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.request[len(f.request)-1]
}

func writeChatConfig(t *testing.T, baseURL, memPath string) string {
	t.Helper()
	yml := "model:\n  base_url: " + baseURL + "\n  name: test-model\nmemory:\n  json: " + memPath + "\n"
	p := filepath.Join(t.TempDir(), "fiveagent.yml")
	if err := os.WriteFile(p, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestChatLoopRepliesAndQuits(t *testing.T) {
	fake := newFakeChatModel(t, "hola, soy FiveAgent")
	mem := filepath.Join(t.TempDir(), "mem.json")
	cfg := writeChatConfig(t, fake.srv.URL, mem)
	var out strings.Builder
	code := runChatIO(cfg, "tester", strings.NewReader("hola\n/salir\n"), &out)
	if code != 0 {
		t.Fatalf("exit code %d, output:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "fiveagent> hola, soy FiveAgent") {
		t.Fatalf("reply missing in output:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "hasta luego") {
		t.Fatalf("quit message missing in output:\n%s", out.String())
	}
}

func TestChatLoopKeepsHistoryAcrossTurns(t *testing.T) {
	fake := newFakeChatModel(t, "primera", "segunda")
	mem := filepath.Join(t.TempDir(), "mem.json")
	cfg := writeChatConfig(t, fake.srv.URL, mem)
	var out strings.Builder
	code := runChatIO(cfg, "tester", strings.NewReader("uno\ndos\n/salir\n"), &out)
	if code != 0 {
		t.Fatalf("exit code %d, output:\n%s", code, out.String())
	}
	var sawFirst bool
	for _, m := range fake.lastRequest() {
		if m.Role == "user" && m.Content == "uno" {
			sawFirst = true
		}
	}
	if !sawFirst {
		t.Fatalf("second turn did not carry the first user message: %+v", fake.lastRequest())
	}
	b, err := os.ReadFile(mem)
	if err != nil {
		t.Fatalf("memory file not written: %v", err)
	}
	if !strings.Contains(string(b), "uno") || !strings.Contains(string(b), "segunda") {
		t.Fatalf("memory file lacks the conversation: %s", b)
	}
}

func TestChatHelpAndEmptyLines(t *testing.T) {
	fake := newFakeChatModel(t, "vale")
	mem := filepath.Join(t.TempDir(), "mem.json")
	cfg := writeChatConfig(t, fake.srv.URL, mem)
	var out strings.Builder
	code := runChatIO(cfg, "tester", strings.NewReader("\n/ayuda\nping\n/salir\n"), &out)
	if code != 0 {
		t.Fatalf("exit code %d, output:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "commands:") {
		t.Fatalf("help text missing:\n%s", out.String())
	}
	if strings.Count(out.String(), "fiveagent>") != 1 {
		t.Fatalf("empty line should not reach the model:\n%s", out.String())
	}
}

func TestChatBadConfig(t *testing.T) {
	var out strings.Builder
	if code := runChatIO(filepath.Join(t.TempDir(), "missing.yml"), "tester", strings.NewReader(""), &out); code != 1 {
		t.Fatalf("expected exit 1 for missing config, got %d", code)
	}
}
