package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
	added, err := t.K.AppendFrom(a.File, a.Entry, "save_memory")
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
		"forget something, or when a stored fact is no longer true. Searches " +
		"every memory file of this scope (the curated files and the session " +
		"digests) and removes every entry containing the given text."
}

func (t ForgetMemory) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"file": {
				"type": "string",
				"enum": ["people", "preferences", "workstreams"],
				"description": "The memory file the fact lives in; the search still covers every memory file of the scope."
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
	// Every file, not just a.File: pruning copies compacted turns into
	// the session digests, and a fact left there resurrects after a
	// restart (battery run 6, M3 0/1).
	n, err := t.K.ForgetAll(a.Match)
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "nothing matched", nil
	}
	return fmt.Sprintf("forgot %d entries", n), nil
}

// SaveLearning lets the model record one short self-critique in the
// learnings file (roadmap stage 7f): when a task fails or the user
// corrects the agent, the lesson is written down in plain words so the
// same mistake is not repeated in later turns.
type SaveLearning struct {
	K *memory.Knowledge
}

func (t SaveLearning) Name() string { return "save_learning" }

func (t SaveLearning) Description() string {
	return "Record one short lesson about your own behavior in long-term memory. " +
		"Use it when a task fails or the user corrects you: write in plain words " +
		"what went wrong and what to do differently, so the same mistake is not " +
		"repeated. Not for facts about the user (use save_memory for those). " +
		"Duplicates are skipped automatically."
}

func (t SaveLearning) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"entry": {
				"type": "string",
				"description": "One short lesson, e.g. 'When asked for the other Taylor, ask which one instead of guessing'."
			}
		},
		"required": ["entry"]
	}`)
}

func (t SaveLearning) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Entry string `json:"entry"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("save_learning: bad arguments: %w", err)
	}
	entry := strings.Join(strings.Fields(a.Entry), " ")
	if len(entry) < 8 {
		return "error: a lesson needs at least a few words", nil
	}
	if len(entry) > 300 {
		entry = entry[:300]
	}
	added, err := t.K.AppendFrom("learnings", entry, "save_learning")
	if err != nil {
		return "", err
	}
	if !added {
		return "already stored", nil
	}
	return "saved", nil
}
