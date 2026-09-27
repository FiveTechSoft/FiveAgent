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
	return &Client{cfg: cfg, http: &http.Client{Timeout: 120 * time.Second}}
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

// Chat sends the conversation (and optional tool specs) and returns the
// assistant message, which may carry content, tool calls, or both.
func (c *Client) Chat(ctx context.Context, msgs []Message, toolSpecs []tools.Spec) (Message, error) {
	var out Message
	body, err := json.Marshal(chatRequest{Model: c.cfg.Name, Messages: msgs, Tools: toolSpecs})
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return out, err
	}
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("model: %s: %s", resp.Status, raw)
	}
	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return out, err
	}
	if len(parsed.Choices) == 0 {
		return out, fmt.Errorf("model: no choices in response")
	}
	return parsed.Choices[0].Message, nil
}
