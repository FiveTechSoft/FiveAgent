// Package google holds the Google service clients of roadmap stage 27.
// Gmail first; Calendar and Drive copy the shape. Base URLs are
// package variables so tests run everything against fake servers.
package google

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// GmailBase is the Gmail API root, overridable in tests.
var GmailBase = "https://gmail.googleapis.com"

// AuthURL / TokenURL are the Google OAuth endpoints, overridable in tests.
var (
	AuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	TokenURL = "https://oauth2.googleapis.com/token"
)

// Gmail reads and writes mail through the Gmail API.
type Gmail struct {
	HTTP *http.Client // OAuth-authorized client (oauth.Client)
	Base string       // empty uses GmailBase
}

func (g *Gmail) base() string {
	if g.Base != "" {
		return g.Base
	}
	return GmailBase
}

// Message is the metadata of one email.
type Message struct {
	ID       string `json:"id"`
	ThreadID string `json:"threadId"`
	Snippet  string `json:"snippet"`
	Subject  string `json:"subject"`
	From     string `json:"from"`
	Date     string `json:"date"`
}

// Search lists messages matching a Gmail query (e.g. "from:ana newer_than:2d").
func (g *Gmail) Search(ctx context.Context, query string, maxResults int) ([]Message, error) {
	if maxResults <= 0 || maxResults > 50 {
		maxResults = 10
	}
	u := g.base() + "/gmail/v1/users/me/messages?q=" + url.QueryEscape(query) + fmt.Sprintf("&maxResults=%d", maxResults)
	raw, err := g.get(ctx, u)
	if err != nil {
		return nil, err
	}
	var list struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	var out []Message
	for _, m := range list.Messages {
		full, err := g.Get(ctx, m.ID)
		if err != nil {
			return out, err
		}
		out = append(out, *full)
	}
	return out, nil
}

// Get fetches one message's metadata.
func (g *Gmail) Get(ctx context.Context, id string) (*Message, error) {
	raw, err := g.get(ctx, g.base()+"/gmail/v1/users/me/messages/"+url.PathEscape(id)+"?format=metadata&metadataHeaders=Subject&metadataHeaders=From&metadataHeaders=Date")
	if err != nil {
		return nil, err
	}
	var parsed struct {
		ID       string `json:"id"`
		ThreadID string `json:"threadId"`
		Snippet  string `json:"snippet"`
		Payload  struct {
			Headers []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"headers"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	m := &Message{ID: parsed.ID, ThreadID: parsed.ThreadID, Snippet: parsed.Snippet}
	for _, h := range parsed.Payload.Headers {
		switch h.Name {
		case "Subject":
			m.Subject = h.Value
		case "From":
			m.From = h.Value
		case "Date":
			m.Date = h.Value
		}
	}
	return m, nil
}

// Send mails a plain-text message. It returns the sent message id.
func (g *Gmail) Send(ctx context.Context, from, to, subject, body string) (string, error) {
	return g.postRFC822(ctx, g.base()+"/gmail/v1/users/me/messages/send", from, to, subject, body)
}

// CreateDraft stores a draft instead of sending. It returns the draft id.
func (g *Gmail) CreateDraft(ctx context.Context, from, to, subject, body string) (string, error) {
	return g.postRFC822(ctx, g.base()+"/gmail/v1/users/me/drafts", from, to, subject, body)
}

func (g *Gmail) postRFC822(ctx context.Context, endpoint, from, to, subject, body string) (string, error) {
	var msg strings.Builder
	fmt.Fprintf(&msg, "From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s", from, to, subject, body)
	payload := map[string]any{
		"message": map[string]string{"raw": base64.URLEncoding.EncodeToString([]byte(msg.String()))},
	}
	if strings.HasSuffix(endpoint, "/send") {
		payload = map[string]any{"raw": base64.URLEncoding.EncodeToString([]byte(msg.String()))}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("gmail: %s", resp.Status)
	}
	var parsed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return "", err
	}
	return parsed.ID, nil
}

func (g *Gmail) get(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gmail: %s", resp.Status)
	}
	return raw, nil
}
