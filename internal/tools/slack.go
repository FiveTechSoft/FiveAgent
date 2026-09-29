package tools

// Slack tools (stage 27d): first non-Google copy of the integration
// shape - authorized client factory with an honest "not connected"
// error, read (channels, history) and write (post).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/FiveTechSoft/FiveAgent/internal/slack"
)

// SlackFor builds an authorized Slack client or explains why it
// cannot.
type SlackFor func(ctx context.Context) (*slack.Client, error)

// SlackChannels lists the channels the bot can see.
type SlackChannels struct {
	Client SlackFor
}

func (t SlackChannels) Name() string { return "slack_channels" }

func (t SlackChannels) Description() string {
	return "List the Slack channels the agent's bot can see, with their ids (needed by slack_read and slack_send)."
}

func (t SlackChannels) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"max_results": {"type": "integer", "description": "1-200, default 50"}
		}
	}`)
}

func (t SlackChannels) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		MaxResults int `json:"max_results"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	c, err := t.Client(ctx)
	if err != nil {
		return "", err
	}
	chs, err := c.ListChannels(ctx, a.MaxResults)
	if err != nil {
		return "", err
	}
	if len(chs) == 0 {
		return "no channels visible to the bot", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d channel(s):\n", len(chs))
	for _, ch := range chs {
		fmt.Fprintf(&b, "- id:%s | #%s\n", ch.ID, ch.Name)
	}
	return b.String(), nil
}

// SlackRead reads recent messages of one channel.
type SlackRead struct {
	Client SlackFor
}

func (t SlackRead) Name() string { return "slack_read" }

func (t SlackRead) Description() string {
	return "Read the recent messages of a Slack channel by id (use slack_channels to find ids)."
}

func (t SlackRead) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"channel": {"type": "string", "description": "channel id, e.g. C0123ABCD"},
			"limit": {"type": "integer", "description": "1-50, default 10"}
		},
		"required": ["channel"]
	}`)
}

func (t SlackRead) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Channel string `json:"channel"`
		Limit   int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	c, err := t.Client(ctx)
	if err != nil {
		return "", err
	}
	msgs, err := c.History(ctx, a.Channel, a.Limit)
	if err != nil {
		return "", err
	}
	if len(msgs) == 0 {
		return "no messages in that channel", nil
	}
	var b strings.Builder
	for _, m := range msgs {
		fmt.Fprintf(&b, "- [%s] %s: %s\n", m.Ts, m.User, m.Text)
	}
	return b.String(), nil
}

// SlackSend posts one message to a channel.
type SlackSend struct {
	Client SlackFor
}

func (t SlackSend) Name() string { return "slack_send" }

func (t SlackSend) Description() string {
	return "Post a message to a Slack channel by id. Confirm the channel and the exact text with the user before calling unless they already approved them."
}

func (t SlackSend) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"channel": {"type": "string", "description": "channel id"},
			"text": {"type": "string"}
		},
		"required": ["channel", "text"]
	}`)
}

func (t SlackSend) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Channel string `json:"channel"`
		Text    string `json:"text"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	c, err := t.Client(ctx)
	if err != nil {
		return "", err
	}
	ts, err := c.PostMessage(ctx, a.Channel, a.Text)
	if err != nil {
		return "", fmt.Errorf("slack_send: %w", err)
	}
	return fmt.Sprintf("message posted (ts %s)", ts), nil
}
