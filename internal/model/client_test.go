package model

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/config"
)

func TestModelTimeoutDefaults(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.Model
		want time.Duration
	}{
		{"explicit wins", config.Model{BaseURL: "http://localhost:11434/v1", Timeout: 42}, 42 * time.Second},
		{"local default 600s", config.Model{BaseURL: "http://localhost:11434/v1"}, 600 * time.Second},
		{"loopback IP default 600s", config.Model{BaseURL: "http://127.0.0.1:8080/v1"}, 600 * time.Second},
		{"remote default 120s", config.Model{BaseURL: "https://api.deepseek.com/v1"}, 120 * time.Second},
		{"empty base_url counts as remote", config.Model{}, 120 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ModelTimeout(c.cfg); got != c.want {
				t.Fatalf("got %s, want %s", got, c.want)
			}
		})
	}
}

func TestChatNumThreadUsesNativeAPI(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		resp := map[string]any{
			"message": map[string]any{
				"role":    "assistant",
				"content": "",
				"tool_calls": []any{map[string]any{
					"function": map[string]any{
						"name":      "lookup",
						"arguments": map[string]any{"city": "Madrid"},
					},
				}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewOpenAICompat(config.Model{BaseURL: srv.URL + "/v1", Name: "qwen3", NumThread: 4})
	msg, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/chat" {
		t.Fatalf("path: got %q, want /api/chat (num_thread must go to the native API)", gotPath)
	}
	opts, ok := gotBody["options"].(map[string]any)
	if !ok {
		t.Fatalf("request carries no options object: %v", gotBody)
	}
	if opts["num_thread"] != float64(4) {
		t.Fatalf("options.num_thread: got %v, want 4", opts["num_thread"])
	}
	if gotBody["stream"] != false {
		t.Fatalf("stream: got %v, want false", gotBody["stream"])
	}
	if gotBody["model"] != "qwen3" {
		t.Fatalf("model: got %v, want qwen3", gotBody["model"])
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("tool calls: got %d, want 1", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].ID == "" {
		t.Fatal("native tool call got no synthesized id")
	}
	if msg.ToolCalls[0].Function.Name != "lookup" {
		t.Fatalf("tool name: got %q", msg.ToolCalls[0].Function.Name)
	}
	if msg.ToolCalls[0].Function.Arguments != `{"city":"Madrid"}` {
		t.Fatalf("arguments: got %q, want compact JSON string", msg.ToolCalls[0].Function.Arguments)
	}
}

func TestChatWithoutNumThreadKeepsOpenAIPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		resp := map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{"role": "assistant", "content": "hola"},
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewOpenAICompat(config.Model{BaseURL: srv.URL + "/v1", Name: "qwen3"})
	msg, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("path: got %q, want /v1/chat/completions", gotPath)
	}
	if msg.Content != "hola" {
		t.Fatalf("content: got %q", msg.Content)
	}
}

func TestChatNumThreadWithoutV1SuffixFailsLoudly(t *testing.T) {
	c := NewOpenAICompat(config.Model{BaseURL: "http://localhost:11434", Name: "qwen3", NumThread: 4})
	_, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
	if err == nil {
		t.Fatal("expected a loud failure, got nil")
	}
	if !strings.Contains(err.Error(), "num_thread") {
		t.Fatalf("error must name num_thread: %v", err)
	}
}
