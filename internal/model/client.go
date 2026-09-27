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
)

// Message is one chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

type chatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

// Chat sends the conversation and returns the assistant reply.
func (c *Client) Chat(ctx context.Context, msgs []Message) (string, error) {
	body, err := json.Marshal(chatRequest{Model: c.cfg.Name, Messages: msgs})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("model: %s: %s", resp.Status, raw)
	}
	var out chatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("model: no choices in response")
	}
	return out.Choices[0].Message.Content, nil
}
