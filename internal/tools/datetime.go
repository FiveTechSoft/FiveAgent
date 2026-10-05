package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Datetime tells the model the current date and time. Without an
// explicit timezone argument it answers in Zone (the configured user
// zone), falling back to the host's local zone - never UTC: a
// late-night ask (00:00-01:59 in Madrid) would name the previous
// weekday (battery run 16, as reported in its CHANGELOG entry).
type Datetime struct {
	Zone *time.Location   // default zone; nil means time.Local
	Now  func() time.Time // clock; nil means time.Now (tests inject it)
}

// Name implements Tool.
func (Datetime) Name() string { return "current_datetime" }

// Description implements Tool.
func (Datetime) Description() string {
	return "Get the current date and time, optionally in a given IANA timezone (e.g. Europe/Madrid); without one it answers in the user's own timezone."
}

// Parameters implements Tool.
func (Datetime) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"timezone": {
				"type": "string",
				"description": "IANA timezone name, e.g. Europe/Madrid. Defaults to the user's own timezone."
			}
		}
	}`)
}

// Execute implements Tool.
func (d Datetime) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Timezone string `json:"timezone"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("bad arguments: %w", err)
		}
	}
	loc := d.Zone
	if loc == nil {
		loc = time.Local
	}
	if a.Timezone != "" {
		l, err := time.LoadLocation(a.Timezone)
		if err != nil {
			return "", fmt.Errorf("unknown timezone %q", a.Timezone)
		}
		loc = l
	}
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}
	return now().In(loc).Format("Monday, 2 January 2006, 15:04:05 MST"), nil
}
