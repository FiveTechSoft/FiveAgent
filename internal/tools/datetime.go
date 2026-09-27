package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Datetime tells the model the current date and time.
type Datetime struct{}

// Name implements Tool.
func (Datetime) Name() string { return "current_datetime" }

// Description implements Tool.
func (Datetime) Description() string {
	return "Get the current date and time, optionally in a given IANA timezone (e.g. Europe/Madrid)."
}

// Parameters implements Tool.
func (Datetime) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"timezone": {
				"type": "string",
				"description": "IANA timezone name, e.g. Europe/Madrid. Defaults to UTC."
			}
		}
	}`)
}

// Execute implements Tool.
func (Datetime) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Timezone string `json:"timezone"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("bad arguments: %w", err)
		}
	}
	loc := time.UTC
	if a.Timezone != "" {
		l, err := time.LoadLocation(a.Timezone)
		if err != nil {
			return "", fmt.Errorf("unknown timezone %q", a.Timezone)
		}
		loc = l
	}
	return time.Now().In(loc).Format("Monday, 2 January 2006, 15:04:05 MST"), nil
}
