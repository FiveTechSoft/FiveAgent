package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/FiveTechSoft/FiveAgent/internal/links"
)

// MakeReportLink mints a signed, PIN-protected link to a report page
// (stage 24): long answers go out as links, not walls of text.
type MakeReportLink struct{ S *links.Service }

func (t MakeReportLink) Name() string { return "make_report_link" }
func (t MakeReportLink) Description() string {
	return "Turn a long answer (report, analysis, battery results) into a short link instead of a wall of text: the page is served by the bot itself, signed, PIN-protected and expiring. Send the user BOTH the link and the PIN in your reply."
}
func (t MakeReportLink) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"content":{"type":"string","description":"the full report text"}},"required":["title","content"]}`)
}
func (t MakeReportLink) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	_, userID := RequestInfo(ctx)
	if userID == "" {
		return "", errors.New("make_report_link: no user in the request context")
	}
	link, pin, err := t.S.MintReport(userID, a.Title, a.Content)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("report link created. Send the user BOTH:\nlink: %s\nPIN: %s", link, pin), nil
}

// MakeFormLink mints a signed, PIN-protected form link for sensitive
// data (tokens, passwords): the value goes straight to the vault.
type MakeFormLink struct{ S *links.Service }

func (t MakeFormLink) Name() string { return "make_form_link" }
func (t MakeFormLink) Description() string {
	return "Ask for sensitive data (tokens, passwords, API keys) with a link to a small form instead of having the user type it in the chat: the value goes straight to the bot's vault, never through the conversation. Send the user BOTH the link and the PIN."
}
func (t MakeFormLink) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"title":{"type":"string","description":"what the data is for, e.g. 'Token de Telegram'"},"vault_key":{"type":"string","description":"vault key to store it under, e.g. telegram_bot_token"}},"required":["title","vault_key"]}`)
}
func (t MakeFormLink) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Title    string `json:"title"`
		VaultKey string `json:"vault_key"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	_, userID := RequestInfo(ctx)
	if userID == "" {
		return "", errors.New("make_form_link: no user in the request context")
	}
	link, pin, err := t.S.MintForm(userID, a.Title, a.VaultKey)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("form link created. Send the user BOTH:\nlink: %s\nPIN: %s", link, pin), nil
}
