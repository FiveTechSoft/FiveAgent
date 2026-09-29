// Package model talks to any OpenAI-compatible chat completions endpoint,
// local (Ollama, llama.cpp, vLLM) or commercial (OpenAI, DeepSeek, ...).
package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

// ToolCall is one function call requested by the model.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON-encoded
	} `json:"function"`
}

// Message is one chat message. Content may be empty on tool-call turns.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"` // role "tool" messages
}

// Client is an OpenAI-compatible chat client.
type Client struct {
	cfg  config.Model
	http *http.Client
}

// NewOpenAICompat builds a client for the configured endpoint.
func NewOpenAICompat(cfg config.Model) *Client {
	return &Client{cfg: cfg, http: &http.Client{Timeout: ModelTimeout(cfg)}}
}

// ModelTimeout picks the per-call timeout: model.timeout from the yml
// wins; otherwise 600s for local endpoints (Ollama loads big models
// into RAM on first use) and 120s for remote ones.
func ModelTimeout(cfg config.Model) time.Duration {
	if cfg.Timeout > 0 {
		return time.Duration(cfg.Timeout) * time.Second
	}
	if u, err := url.Parse(cfg.BaseURL); err == nil {
		switch u.Hostname() {
		case "localhost", "127.0.0.1", "::1":
			return 600 * time.Second
		}
	}
	return 120 * time.Second
}

type chatRequest struct {
	Model    string       `json:"model"`
	Messages []Message    `json:"messages"`
	Tools    []tools.Spec `json:"tools,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

// ollamaNativeChatURL derives Ollama's native chat endpoint from the
// OpenAI-compatible base_url (.../v1 -> .../api/chat). The native
// endpoint is the only one that honors per-request options such as
// num_thread: the OpenAI-compatible layer builds its own options map
// and silently drops everything else, so sending num_thread there
// would be a no-op. ok=false when base has no /v1 suffix to replace.
func ollamaNativeChatURL(base string) (string, bool) {
	if !strings.HasSuffix(base, "/v1") {
		return "", false
	}
	return strings.TrimSuffix(base, "/v1") + "/api/chat", true
}

// ollamaChatRequest is the native /api/chat payload. Options carries
// the runner knobs - num_thread caps the inference threads so the
// runner stays a good neighbor on shared CPU hosts.
type ollamaChatRequest struct {
	Model    string         `json:"model"`
	Messages []Message      `json:"messages"`
	Tools    []tools.Spec   `json:"tools,omitempty"`
	Stream   bool           `json:"stream"`
	Options  map[string]any `json:"options"`
}

// ollamaChatResponse is the native reply. Tool-call arguments arrive
// as a JSON object, not a string, and calls carry no id.
type ollamaChatResponse struct {
	Message struct {
		Role      string `json:"role"`
		Content   string `json:"content"`
		ToolCalls []struct {
			Function struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	} `json:"message"`
}

// Chat sends the conversation (and optional tool specs) and returns the
// assistant message, which may carry content, tool calls, or both.
func (c *Client) Chat(ctx context.Context, msgs []Message, toolSpecs []tools.Spec) (Message, error) {
	if c.cfg.NumThread > 0 {
		return c.ollamaNativeChat(ctx, msgs, toolSpecs)
	}
	var out Message
	raw, err := c.post(ctx, c.cfg.BaseURL+"/chat/completions",
		chatRequest{Model: c.cfg.Name, Messages: msgs, Tools: toolSpecs})
	if err != nil {
		return out, err
	}
	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return out, &Failure{Kind: FailureMalformed, Err: err}
	}
	if len(parsed.Choices) == 0 {
		return out, &Failure{Kind: FailureMalformed, Err: fmt.Errorf("no choices in response")}
	}
	return parsed.Choices[0].Message, nil
}

// ollamaNativeChat serves Chat when num_thread is set: only Ollama's
// native /api/chat honors per-request options, so the request goes
// there instead of the OpenAI-compatible endpoint.
func (c *Client) ollamaNativeChat(ctx context.Context, msgs []Message, toolSpecs []tools.Spec) (Message, error) {
	var out Message
	url, ok := ollamaNativeChatURL(c.cfg.BaseURL)
	if !ok {
		return out, &Failure{Kind: FailureUnavailable, Err: fmt.Errorf("num_thread requires an Ollama base_url ending in /v1; got %q", c.cfg.BaseURL)}
	}
	raw, err := c.post(ctx, url, ollamaChatRequest{
		Model:    c.cfg.Name,
		Messages: msgs,
		Tools:    toolSpecs,
		Stream:   false,
		Options:  map[string]any{"num_thread": c.cfg.NumThread},
	})
	if err != nil {
		return out, err
	}
	var parsed ollamaChatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return out, &Failure{Kind: FailureMalformed, Err: err}
	}
	out.Role = parsed.Message.Role
	out.Content = parsed.Message.Content
	for i, tc := range parsed.Message.ToolCalls {
		args, err := json.Marshal(tc.Function.Arguments)
		if err != nil {
			return Message{}, &Failure{Kind: FailureMalformed, Err: err}
		}
		call := ToolCall{ID: fmt.Sprintf("call_%d", i), Type: "function"}
		call.Function.Name = tc.Function.Name
		call.Function.Arguments = string(args)
		out.ToolCalls = append(out.ToolCalls, call)
	}
	return out, nil
}

// post sends one JSON chat payload and returns the raw response body,
// mapping transport and status failures onto Failure.
func (c *Client) post(ctx context.Context, url string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &Failure{Kind: classifyNetError(err), Err: err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &Failure{Kind: FailureUnavailable, Status: resp.StatusCode, Err: err}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &Failure{
			Kind:   classifyStatus(resp.StatusCode, raw),
			Status: resp.StatusCode,
			Err:    fmt.Errorf("%s: %s", resp.Status, cutRunes(string(raw), 200)),
		}
	}
	return raw, nil
}

// cutRunes truncates s to at most n bytes without splitting a UTF-8
// rune, so an error body can be quoted safely.
func cutRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && (s[n]&0xC0) == 0x80 {
		n--
	}
	return s[:n]
}
