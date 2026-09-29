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

// TestChatNativeToolRoundTrip covers the flow that broke against live
// Ollama: first turn returns a tool call, second turn carries the
// assistant tool_calls and the tool result in the history. The native
// API rejects arguments sent as a string; they must be a JSON object.
func TestChatNativeToolRoundTrip(t *testing.T) {
	call := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if call == 1 {
			json.NewEncoder(w).Encode(map[string]any{
				"message": map[string]any{
					"role":    "assistant",
					"content": "",
					"tool_calls": []any{map[string]any{
						"function": map[string]any{
							"name":      "web_search",
							"arguments": map[string]any{"query": "weather madrid"},
						},
					}},
				},
			})
			return
		}
		// Second call: assert the history is native-shaped.
		msgs, ok := body["messages"].([]any)
		if !ok || len(msgs) != 3 {
			t.Errorf("second call history: got %v", body["messages"])
			return
		}
		assistant, _ := msgs[1].(map[string]any)
		tcs, _ := assistant["tool_calls"].([]any)
		if len(tcs) != 1 {
			t.Errorf("assistant tool_calls: got %v", assistant)
			return
		}
		fn, _ := tcs[0].(map[string]any)["function"].(map[string]any)
		args, isObject := fn["arguments"].(map[string]any)
		if !isObject {
			t.Errorf("arguments must be a JSON object on the native wire, got %T: %v", fn["arguments"], fn["arguments"])
		} else if args["query"] != "weather madrid" {
			t.Errorf("arguments.query: got %v", args["query"])
		}
		toolMsg, _ := msgs[2].(map[string]any)
		if toolMsg["role"] != "tool" {
			t.Errorf("tool message role: got %v", toolMsg["role"])
		}
		if toolMsg["content"] != "sunny" {
			t.Errorf("tool message content: got %v", toolMsg["content"])
		}
		json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{"role": "assistant", "content": "It is sunny in Madrid."},
		})
	}))
	defer srv.Close()

	c := NewOpenAICompat(config.Model{BaseURL: srv.URL + "/v1", Name: "qwen3", NumThread: 4})
	history := []Message{{Role: "user", Content: "weather in madrid?"}}
	first, err := c.Chat(context.Background(), history, nil)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if len(first.ToolCalls) != 1 {
		t.Fatalf("first call tool calls: got %d", len(first.ToolCalls))
	}
	history = append(history, first)
	history = append(history, Message{Role: "tool", Content: "sunny", ToolCallID: first.ToolCalls[0].ID})
	second, err := c.Chat(context.Background(), history, nil)
	if err != nil {
		t.Fatalf("second call (the one live Ollama 400'd): %v", err)
	}
	if second.Content != "It is sunny in Madrid." {
		t.Fatalf("second call content: got %q", second.Content)
	}
}
