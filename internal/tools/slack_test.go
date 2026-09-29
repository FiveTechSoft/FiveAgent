package tools

// Slack tools tests (stage 27d): honest not-connected errors, arg
// wiring, formatted output.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/slack"
)

func slackNotConnected(ctx context.Context) (*slack.Client, error) {
	return nil, fmt.Errorf("[slack not connected - ask the owner to open /oauth/slack/start]")
}

func TestSlackToolsHonestWhenNotConnected(t *testing.T) {
	for name, tool := range map[string]Tool{
		"slack_channels": SlackChannels{Client: slackNotConnected},
		"slack_read":     SlackRead{Client: slackNotConnected},
		"slack_send":     SlackSend{Client: slackNotConnected},
	} {
		_, err := tool.Execute(context.Background(), []byte(`{"channel":"C1","text":"hi"}`))
		if err == nil || !strings.Contains(err.Error(), "not connected") {
			t.Fatalf("%s should surface the honest not-connected error, got: %v", name, err)
		}
	}
}

func TestSlackChannelsFormats(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":true,"channels":[{"id":"C1","name":"general"}]}`)
	}))
	defer srv.Close()
	tool := SlackChannels{Client: func(ctx context.Context) (*slack.Client, error) {
		return &slack.Client{HTTP: srv.Client(), Root: srv.URL}, nil
	}}
	out, err := tool.Execute(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "#general") || !strings.Contains(out, "id:C1") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestSlackSendReportsTs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":true,"ts":"1727.9"}`)
	}))
	defer srv.Close()
	tool := SlackSend{Client: func(ctx context.Context) (*slack.Client, error) {
		return &slack.Client{HTTP: srv.Client(), Root: srv.URL}, nil
	}}
	out, err := tool.Execute(context.Background(), []byte(`{"channel":"C1","text":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1727.9") {
		t.Fatalf("send should report the ts: %q", out)
	}
}

func TestSlackSendRequiresText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":false,"error":"no_text"}`)
	}))
	defer srv.Close()
	tool := SlackSend{Client: func(ctx context.Context) (*slack.Client, error) {
		return &slack.Client{HTTP: srv.Client(), Root: srv.URL}, nil
	}}
	_, err := tool.Execute(context.Background(), []byte(`{"channel":"C1","text":"  "}`))
	if err == nil || !strings.Contains(err.Error(), "text is required") {
		t.Fatalf("empty text should be rejected: %v", err)
	}
}
