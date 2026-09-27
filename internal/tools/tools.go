// Package tools holds the agent's tools and the registry the agent loop
// uses to expose them to the model (OpenAI-style function calling).
package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// Tool is one capability the model can invoke.
type Tool interface {
	// Name is the function name the model calls (snake_case).
	Name() string
	// Description tells the model when to use the tool.
	Description() string
	// Parameters is the JSON Schema of the arguments (empty object if none).
	Parameters() json.RawMessage
	// Execute runs the tool with the JSON-encoded arguments.
	Execute(ctx context.Context, args json.RawMessage) (string, error)
}

// Registry is the set of tools available to the agent.
type Registry struct {
	tools  []Tool
	byName map[string]Tool
}

// NewRegistry builds a registry from the given tools.
func NewRegistry(ts ...Tool) *Registry {
	r := &Registry{byName: make(map[string]Tool, len(ts))}
	for _, t := range ts {
		r.tools = append(r.tools, t)
		r.byName[t.Name()] = t
	}
	return r
}

// Spec describes one tool for the chat completions request.
type Spec struct {
	Type     string `json:"type"` // "function"
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

// Specs returns the tool specs to send to the model, nil if empty.
func (r *Registry) Specs() []Spec {
	if r == nil || len(r.tools) == 0 {
		return nil
	}
	out := make([]Spec, 0, len(r.tools))
	for _, t := range r.tools {
		var s Spec
		s.Type = "function"
		s.Function.Name = t.Name()
		s.Function.Description = t.Description()
		p := t.Parameters()
		if len(p) == 0 {
			p = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		s.Function.Parameters = p
		out = append(out, s)
	}
	return out
}

// Execute runs the named tool. Unknown tool names return an error the
// agent loop feeds back to the model as the tool result.
func (r *Registry) Execute(ctx context.Context, name string, args json.RawMessage) (string, error) {
	t, ok := r.byName[name]
	if !ok {
		return "", fmt.Errorf("unknown tool %q", name)
	}
	return t.Execute(ctx, args)
}
