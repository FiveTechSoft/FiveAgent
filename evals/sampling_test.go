package evals

// Stage 11a battery case ("per-kind sampling"): the agent sends
// tool_temperature on rounds that offer tools and chat_temperature on
// the no-tools answer retry, with presence_penalty on both. A request
// body without the right value fails the test; an agent with no
// sampling configured sends neither key, so provider defaults apply.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/agent"
	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/memory"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

// samplingServer answers an empty assistant turn while tools are
// offered (forcing the no-tools answer retry) and a real answer
// without tools. It records every request body.
type samplingServer struct{ bodies []string }

func (s *samplingServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.bodies = append(s.bodies, string(body))
	w.Header().Set("Content-Type", "application/json")
	if strings.Contains(string(body), `"tools":`) {
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":""}}]}`)
		return
	}
	io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"all set"}}]}`)
}

func tempOf(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	return m
}

func TestPerKindSampling(t *testing.T) {
	dir := t.TempDir()
	store, err := memory.OpenJSON(dir + "/history.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := &samplingServer{}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	cfg := &config.Config{Model: config.Model{BaseURL: ts.URL, Name: "eval"}}
	toolT, chatT, presence := 0.1, 0.6, 0.3
	a := agent.New(model.NewOpenAICompat(cfg.Model), store,
		tools.NewRegistry(tools.SaveMemory{}), agent.SystemPrompt(cfg))
	a.WithSampling(config.Sampling{
		ToolTemperature: &toolT, ChatTemperature: &chatT, PresencePenalty: &presence,
	})
	if _, err := a.Handle(context.Background(), "whatsapp", "user-a", "hola"); err != nil {
		t.Fatal(err)
	}
	if len(srv.bodies) < 2 {
		t.Fatalf("expected the tool round plus the answer retry, got %d requests", len(srv.bodies))
	}
	var sawToolRound, sawChatRound bool
	for _, b := range srv.bodies {
		m := tempOf(t, b)
		if pp, ok := m["presence_penalty"]; !ok || pp != presence {
			t.Fatalf("presence_penalty missing or wrong in body: %v", m)
		}
		if strings.Contains(b, `"tools":`) {
			sawToolRound = true
			if m["temperature"] != toolT {
				t.Fatalf("tool round carried temperature %v, want %v", m["temperature"], toolT)
			}
		} else {
			sawChatRound = true
			if m["temperature"] != chatT {
				t.Fatalf("answer retry carried temperature %v, want %v", m["temperature"], chatT)
			}
		}
	}
	if !sawToolRound || !sawChatRound {
		t.Fatalf("sawToolRound=%v sawChatRound=%v; the battery needs both kinds", sawToolRound, sawChatRound)
	}
}

func TestNoSamplingSendsNoKeys(t *testing.T) {
	dir := t.TempDir()
	store, err := memory.OpenJSON(dir + "/history.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := &samplingServer{}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	cfg := &config.Config{Model: config.Model{BaseURL: ts.URL, Name: "eval"}}
	a := agent.New(model.NewOpenAICompat(cfg.Model), store,
		tools.NewRegistry(tools.SaveMemory{}), agent.SystemPrompt(cfg))
	if _, err := a.Handle(context.Background(), "whatsapp", "user-a", "hola"); err != nil {
		t.Fatal(err)
	}
	for _, b := range srv.bodies {
		if strings.Contains(b, "temperature") || strings.Contains(b, "presence_penalty") {
			t.Fatalf("unset sampling must not appear in the request body: %s", b)
		}
	}
}
