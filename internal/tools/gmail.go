package tools

// Gmail tools (stage 27a): the agent searches and sends mail through
// the user's connected account. Without a connected account the tool
// answers an honest "not connected" error - never an empty result
// presented as "no mail".

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/FiveTechSoft/FiveAgent/internal/google"
)

// GmailFor builds an authorized Gmail client or explains why it
// cannot (not connected, refresh failed).
type GmailFor func(ctx context.Context) (*google.Gmail, error)

// GmailSearch searches the user's mailbox.
type GmailSearch struct {
	Client GmailFor
}

func (t GmailSearch) Name() string { return "gmail_search" }

func (t GmailSearch) Description() string {
	return "Search the user's Gmail and list matching messages (from, subject, date, snippet). Use Gmail query syntax, e.g. 'from:ana newer_than:2d' or 'subject:invoice'."
}

func (t GmailSearch) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {"type": "string", "description": "Gmail search query"},
			"max_results": {"type": "integer", "description": "1-50, default 10"}
		},
		"required": ["query"]
	}`)
}

func (t GmailSearch) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Query) == "" {
		return "", fmt.Errorf("gmail_search: query is required")
	}
	g, err := t.Client(ctx)
	if err != nil {
		return "", err
	}
	msgs, err := g.Search(ctx, a.Query, a.MaxResults)
	if err != nil {
		return "", err
	}
	if len(msgs) == 0 {
		return fmt.Sprintf("no messages match %q", a.Query), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d message(s):\n", len(msgs))
	for _, m := range msgs {
		fmt.Fprintf(&b, "- id:%s | %s | %s | %s\n  %s\n", m.ID, m.Date, m.From, m.Subject, m.Snippet)
	}
	return b.String(), nil
}

// GmailSend sends a plain-text email as the user.
type GmailSend struct {
	Client GmailFor
}

func (t GmailSend) Name() string { return "gmail_send" }

func (t GmailSend) Description() string {
	return "Send a plain-text email from the user's Gmail. Confirm recipient, subject and body with the user before calling unless they already approved the exact message."
}

func (t GmailSend) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"to": {"type": "string"},
			"subject": {"type": "string"},
			"body": {"type": "string"}
		},
		"required": ["to", "subject", "body"]
	}`)
}

func (t GmailSend) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		To      string `json:"to"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.To) == "" || strings.TrimSpace(a.Subject) == "" {
		return "", fmt.Errorf("gmail_send: to and subject are required")
	}
	g, err := t.Client(ctx)
	if err != nil {
		return "", err
	}
	// The Gmail API needs no From for /me sends; the server fills it.
	id, err := g.Send(ctx, "me", a.To, a.Subject, a.Body)
	if err != nil {
		return "", fmt.Errorf("gmail_send: %w", err)
	}
	return fmt.Sprintf("email sent to %s (id %s)", a.To, id), nil
}
