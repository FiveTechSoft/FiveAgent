// Package slack is the first non-Google integration (roadmap stage
// 27d): the same shape as the Google clients - overridable base URL,
// OAuth-authorized HTTP client from the shared oauth package, read
// (channels, history) and write (post). It exists to prove the
// pattern outside one vendor's API style.
//
// Slack quirk handled honestly: the API answers 200 OK with
// {"ok": false, "error": "..."} for application errors, so every
// call checks the ok flag instead of trusting the HTTP status.
package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Base is the Slack API root, overridable in tests.
var Base = "https://slack.com/api"

// OAuth endpoints for the shared oauth package.
var (
	AuthURL  = "https://slack.com/oauth/v2/authorize"
	TokenURL = "https://slack.com/api/oauth.v2.access"
)

// Client reads and writes Slack.
type Client struct {
	HTTP *http.Client
	Root string // empty uses Base
}

func (c *Client) base() string {
	if c.Root != "" {
		return c.Root
	}
	return Base
}

// Channel is one Slack conversation.
type Channel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Message is one Slack message.
type Message struct {
	User string `json:"user"`
	Text string `json:"text"`
	Ts   string `json:"ts"`
}

type envelope struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

func check(raw []byte, what string) error {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("slack: %s: %w", what, err)
	}
	if !env.OK {
		return fmt.Errorf("slack: %s: %s", what, env.Error)
	}
	return nil
}

// ListChannels returns the public channels the bot can see.
func (c *Client) ListChannels(ctx context.Context, maxResults int) ([]Channel, error) {
	if maxResults <= 0 || maxResults > 200 {
		maxResults = 50
	}
	u := c.base() + "/conversations.list?limit=" + strconv.Itoa(maxResults)
	raw, err := c.do(ctx, http.MethodGet, u, nil, "")
	if err != nil {
		return nil, err
	}
	if err := check(raw, "conversations.list"); err != nil {
		return nil, err
	}
	var parsed struct {
		Channels []Channel `json:"channels"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	return parsed.Channels, nil
}

// History returns the latest messages of one channel.
func (c *Client) History(ctx context.Context, channel string, limit int) ([]Message, error) {
	if strings.TrimSpace(channel) == "" {
		return nil, fmt.Errorf("slack: channel id is required")
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	u := c.base() + "/conversations.history?channel=" + url.QueryEscape(channel) +
		"&limit=" + strconv.Itoa(limit)
	raw, err := c.do(ctx, http.MethodGet, u, nil, "")
	if err != nil {
		return nil, err
	}
	if err := check(raw, "conversations.history"); err != nil {
		return nil, err
	}
	var parsed struct {
		Messages []Message `json:"messages"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	return parsed.Messages, nil
}

// PostMessage sends one message and returns its timestamp id.
func (c *Client) PostMessage(ctx context.Context, channel, text string) (string, error) {
	if strings.TrimSpace(channel) == "" {
		return "", fmt.Errorf("slack: channel id is required")
	}
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("slack: text is required")
	}
	body, err := json.Marshal(map[string]string{"channel": channel, "text": text})
	if err != nil {
		return "", err
	}
	raw, err := c.do(ctx, http.MethodPost, c.base()+"/chat.postMessage", body, "application/json")
	if err != nil {
		return "", err
	}
	if err := check(raw, "chat.postMessage"); err != nil {
		return "", err
	}
	var parsed struct {
		Ts string `json:"ts"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", err
	}
	return parsed.Ts, nil
}

func (c *Client) do(ctx context.Context, method, u string, body []byte, contentType string) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("slack: %s", resp.Status)
	}
	return raw, nil
}
