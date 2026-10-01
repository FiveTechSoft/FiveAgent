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
	// Temperature and PresencePenalty are the stage 11a per-call
	// sampling knobs; nil leaves them out of the body so the
	// provider's defaults apply.
	Temperature     *float64 `json:"temperature,omitempty"`
	PresencePenalty *float64 `json:"presence_penalty,omitempty"`
	// ChatTemplateKwargs carries template switches such as
	// enable_thinking:false for reasoning models on SGLang/vLLM.
	// Set it only for endpoints that accept it: strict servers may
	// reject unknown fields, and the error surfaces as a 400.
	ChatTemplateKwargs map[string]any `json:"chat_template_kwargs,omitempty"`
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

// ollamaMessage is the native wire shape. Unlike the OpenAI-compatible
// endpoint, the native API requires tool-call arguments as a JSON
// OBJECT in the history (api.ToolCallFunctionArguments unmarshals into
// an ordered map): sending our internal string form makes Ollama reject
// the second turn with HTTP 400 ("can't find closing '}' symbol").
type ollamaMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content"`
	ToolCalls  []ollamaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type ollamaToolCall struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type"` // "function"
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

// toOllamaMessages converts the internal history to the native wire
// shape, decoding each tool call's argument string into a JSON object.
func toOllamaMessages(msgs []Message) ([]ollamaMessage, error) {
	out := make([]ollamaMessage, 0, len(msgs))
	for _, m := range msgs {
		om := ollamaMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			args := json.RawMessage(tc.Function.Arguments)
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			if !json.Valid(args) {
				return nil, fmt.Errorf("tool call %s: arguments are not valid JSON: %q", tc.Function.Name, cutRunes(tc.Function.Arguments, 100))
			}
			call := ollamaToolCall{ID: tc.ID, Type: "function"}
			call.Function.Name = tc.Function.Name
			call.Function.Arguments = args
			om.ToolCalls = append(om.ToolCalls, call)
		}
		out = append(out, om)
	}
	return out, nil
}

// ollamaChatRequest is the native /api/chat payload. Options carries
// the runner knobs - num_thread caps the inference threads so the
// runner stays a good neighbor on shared CPU hosts.
type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Tools    []tools.Spec    `json:"tools,omitempty"`
	Stream   bool            `json:"stream"`
	Options  map[string]any  `json:"options"`
}

// ollamaChatResponse is the native reply. Tool-call arguments arrive
// as a JSON object, not a string, and calls carry no id.
type ollamaChatResponse struct {
	Model              string `json:"model"`
	Done               bool   `json:"done"`
	DoneReason         string `json:"done_reason"`
	PromptEvalCount    int    `json:"prompt_eval_count"`
	EvalCount          int    `json:"eval_count"`
	TotalDuration      int64  `json:"total_duration"`
	LoadDuration       int64  `json:"load_duration"`
	PromptEvalDuration int64  `json:"prompt_eval_duration"`
	EvalDuration       int64  `json:"eval_duration"`
	Message            struct {
		Role      string `json:"role"`
		Content   string `json:"content"`
		Thinking  string `json:"thinking"`
		ToolCalls []struct {
			Function struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	} `json:"message"`
}

// CallOptions carries the per-call sampling knobs (stage 11a). Nil
// fields are not sent.
type CallOptions struct {
	Temperature     *float64
	PresencePenalty *float64
}

// Chat sends the conversation (and optional tool specs) and returns the
// assistant message, which may carry content, tool calls, or both.
func (c *Client) Chat(ctx context.Context, msgs []Message, toolSpecs []tools.Spec) (Message, error) {
	return c.ChatWithOptions(ctx, msgs, toolSpecs, nil)
}

// ChatWithOptions is Chat with per-call sampling (stage 11a): tool
// rounds run cooler, final answers warmer. On Ollama's native route the
// values ride the options map next to num_thread.
func (c *Client) ChatWithOptions(ctx context.Context, msgs []Message, toolSpecs []tools.Spec, opts *CallOptions) (Message, error) {
	if c.cfg.NumThread > 0 {
		return c.ollamaNativeChat(ctx, msgs, toolSpecs, opts)
	}
	var out Message
	req := chatRequest{Model: c.cfg.Name, Messages: msgs, Tools: toolSpecs, ChatTemplateKwargs: c.cfg.ChatTemplateKwargs}
	if opts != nil {
		req.Temperature = opts.Temperature
		req.PresencePenalty = opts.PresencePenalty
	}
	raw, err := c.post(ctx, c.cfg.BaseURL+"/chat/completions", req)
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
func (c *Client) ollamaNativeChat(ctx context.Context, msgs []Message, toolSpecs []tools.Spec, opts *CallOptions) (Message, error) {
	var out Message
	url, ok := ollamaNativeChatURL(c.cfg.BaseURL)
	if !ok {
		return out, &Failure{Kind: FailureUnavailable, Err: fmt.Errorf("num_thread requires an Ollama base_url ending in /v1; got %q", c.cfg.BaseURL)}
	}
	nativeMsgs, err := toOllamaMessages(msgs)
	if err != nil {
		return out, &Failure{Kind: FailureMalformed, Err: err}
	}
	nativeOpts := map[string]any{"num_thread": c.cfg.NumThread}
	if opts != nil {
		if opts.Temperature != nil {
			nativeOpts["temperature"] = *opts.Temperature
		}
		if opts.PresencePenalty != nil {
			nativeOpts["presence_penalty"] = *opts.PresencePenalty
		}
	}
	observation := NativeObservation{Model: c.cfg.Name, Purpose: modelPurpose(ctx)}
	if n, ok := nativeOpts["num_ctx"].(int); ok {
		observation.RequestedNumCtx = &n
	}
	if n, ok := nativeOpts["num_predict"].(int); ok {
		observation.RequestedNumPredict = &n
	}
	defer func() { observeNative(ctx, observation) }()
	raw, status, err := c.postWithStatus(ctx, url, ollamaChatRequest{
		Model:    c.cfg.Name,
		Messages: nativeMsgs,
		Tools:    toolSpecs,
		Stream:   false,
		Options:  nativeOpts,
	})
	observation.HTTPStatus = status
	if err != nil {
		observation.Error = err.Error()
		return out, err
	}
	var parsed ollamaChatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		observation.Error = err.Error()
		return out, &Failure{Kind: FailureMalformed, Err: err}
	}
	observation.Model = parsed.Model
	observation.Done = parsed.Done
	observation.DoneReason = parsed.DoneReason
	observation.Thinking = parsed.Message.Thinking
	observation.Content = parsed.Message.Content
	observation.ToolCalls = len(parsed.Message.ToolCalls)
	observation.PromptEvalCount = parsed.PromptEvalCount
	observation.EvalCount = parsed.EvalCount
	observation.TotalDuration = parsed.TotalDuration
	observation.LoadDuration = parsed.LoadDuration
	observation.PromptEvalDuration = parsed.PromptEvalDuration
	observation.EvalDuration = parsed.EvalDuration
	if parsed.DoneReason == "length" {
		err := &Failure{Kind: FailureGenerationLimit, Err: fmt.Errorf("native generation ended at length limit (prompt=%d, generated=%d)", parsed.PromptEvalCount, parsed.EvalCount)}
		observation.Error = err.Error()
		return out, err // never deliver or execute a possibly truncated response
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
	raw, _, err := c.postWithStatus(ctx, url, payload)
	return raw, err
}

func (c *Client) postWithStatus(ctx context.Context, url string, payload any) ([]byte, int, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, &Failure{Kind: classifyNetError(err), Err: err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, &Failure{Kind: FailureUnavailable, Status: resp.StatusCode, Err: err}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, &Failure{
			Kind:   classifyStatus(resp.StatusCode, raw),
			Status: resp.StatusCode,
			Err:    fmt.Errorf("%s: %s", resp.Status, cutRunes(string(raw), 200)),
		}
	}
	return raw, resp.StatusCode, nil
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
