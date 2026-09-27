package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/FiveTechSoft/FiveAgent/internal/memory"
)

// SaveMemory lets the model store one durable fact in long-term memory.
type SaveMemory struct {
	K *memory.Knowledge
}

func (t SaveMemory) Name() string { return "save_memory" }

func (t SaveMemory) Description() string {
	return "Store one durable fact in long-term memory (a person, a preference, " +
		"or the state of ongoing work). Use it when the user tells you something " +
		"worth remembering across conversations, or asks you to remember. " +
		"Duplicates are skipped automatically."
}

func (t SaveMemory) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"file": {
				"type": "string",
				"enum": ["people", "preferences", "workstreams"],
				"description": "Which memory file: people for who the user knows, preferences for likes/dislikes, workstreams for ongoing work."
			},
			"entry": {
				"type": "string",
				"description": "One short fact, e.g. 'María is his sister' or 'Prefers tea over coffee'."
			}
		},
		"required": ["file", "entry"]
	}`)
}

func (t SaveMemory) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		File  string `json:"file"`
		Entry string `json:"entry"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("save_memory: bad arguments: %w", err)
	}
	added, err := t.K.Append(a.File, a.Entry)
	if err != nil {
		return "", err
	}
	if !added {
		return "already stored", nil
	}
	return "saved", nil
}

// ForgetMemory lets the model remove facts from long-term memory.
type ForgetMemory struct {
	K *memory.Knowledge
}

func (t ForgetMemory) Name() string { return "forget_memory" }

func (t ForgetMemory) Description() string {
	return "Remove facts from long-term memory. Use it when the user asks to " +
		"forget something, or when a stored fact is no longer true. Removes " +
		"every entry containing the given text."
}

func (t ForgetMemory) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"file": {
				"type": "string",
				"enum": ["people", "preferences", "workstreams"],
				"description": "Which memory file to search."
			},
			"match": {
				"type": "string",
				"description": "Text contained in the entries to remove, e.g. 'pasta'."
			}
		},
		"required": ["file", "match"]
	}`)
}

func (t ForgetMemory) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		File  string `json:"file"`
		Match string `json:"match"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("forget_memory: bad arguments: %w", err)
	}
	n, err := t.K.Forget(a.File, a.Match)
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "nothing matched", nil
	}
	return fmt.Sprintf("forgot %d entries", n), nil
}
