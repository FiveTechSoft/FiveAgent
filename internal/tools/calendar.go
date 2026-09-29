package tools

// Calendar tools (stage 27b): same shape as the Gmail tools - an
// authorized client factory that answers an honest "not connected"
// error, read and write tools.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/google"
)

// CalendarFor builds an authorized Calendar client or explains why it
// cannot.
type CalendarFor func(ctx context.Context) (*google.Calendar, error)

// CalendarList lists events in a date range.
type CalendarList struct {
	Client CalendarFor
}

func (t CalendarList) Name() string { return "calendar_list" }

func (t CalendarList) Description() string {
	return "List the user's calendar events in a date range (RFC3339 or '2006-01-02'). Use it for agenda questions: what is on today, this week, next Friday."
}

func (t CalendarList) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"from": {"type": "string", "description": "range start: RFC3339 or 2006-01-02"},
			"to": {"type": "string", "description": "range end, same formats"},
			"max_results": {"type": "integer", "description": "1-50, default 10"}
		},
		"required": ["from", "to"]
	}`)
}

// parseWhen accepts RFC3339 or a bare date.
func parseWhen(s string, endOfDay bool) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	d, err := time.ParseInLocation("2006-01-02", s, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("cannot parse %q - use RFC3339 or 2006-01-02", s)
	}
	if endOfDay {
		d = d.Add(24*time.Hour - time.Second)
	}
	return d, nil
}

func (t CalendarList) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		From       string `json:"from"`
		To         string `json:"to"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	from, err := parseWhen(a.From, false)
	if err != nil {
		return "", err
	}
	to, err := parseWhen(a.To, true)
	if err != nil {
		return "", err
	}
	c, err := t.Client(ctx)
	if err != nil {
		return "", err
	}
	events, err := c.ListEvents(ctx, from, to, a.MaxResults)
	if err != nil {
		return "", err
	}
	if len(events) == 0 {
		return "no events in that range", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d event(s):\n", len(events))
	for _, ev := range events {
		loc := ""
		if ev.Location != "" {
			loc = " @ " + ev.Location
		}
		fmt.Fprintf(&b, "- id:%s | %s -> %s | %s%s\n", ev.ID, ev.Start, ev.End, ev.Summary, loc)
	}
	return b.String(), nil
}

// CalendarCreate creates one event.
type CalendarCreate struct {
	Client CalendarFor
}

func (t CalendarCreate) Name() string { return "calendar_create" }

func (t CalendarCreate) Description() string {
	return "Create a calendar event (summary, start, end, optional location, description and attendees). Confirm the details with the user before calling unless they already approved them."
}

func (t CalendarCreate) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"summary": {"type": "string"},
			"start": {"type": "string", "description": "RFC3339"},
			"end": {"type": "string", "description": "RFC3339"},
			"location": {"type": "string"},
			"description": {"type": "string"},
			"attendees": {"type": "array", "items": {"type": "string"}}
		},
		"required": ["summary", "start", "end"]
	}`)
}

func (t CalendarCreate) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Summary     string   `json:"summary"`
		Start       string   `json:"start"`
		End         string   `json:"end"`
		Location    string   `json:"location"`
		Description string   `json:"description"`
		Attendees   []string `json:"attendees"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Summary) == "" {
		return "", fmt.Errorf("calendar_create: summary is required")
	}
	if _, err := time.Parse(time.RFC3339, a.Start); err != nil {
		return "", fmt.Errorf("calendar_create: start must be RFC3339: %w", err)
	}
	if _, err := time.Parse(time.RFC3339, a.End); err != nil {
		return "", fmt.Errorf("calendar_create: end must be RFC3339: %w", err)
	}
	c, err := t.Client(ctx)
	if err != nil {
		return "", err
	}
	id, err := c.CreateEvent(ctx, google.Event{
		Summary: a.Summary, Start: a.Start, End: a.End,
		Location: a.Location, Attendees: a.Attendees,
	}, a.Description)
	if err != nil {
		return "", fmt.Errorf("calendar_create: %w", err)
	}
	return fmt.Sprintf("event %q created (id %s)", a.Summary, id), nil
}
