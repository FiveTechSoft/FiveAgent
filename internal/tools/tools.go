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

// Add registers t unless the name is already taken. The agent uses it
// to self-register its built-ins (stage 18's run_subtask) into the
// caller's registry.
func (r *Registry) Add(t Tool) {
	if _, ok := r.byName[t.Name()]; ok {
		return
	}
	r.tools = append(r.tools, t)
	r.byName[t.Name()] = t
}

// Without returns a copy of the registry excluding the named tools.
// Sub-turns use it to cap the delegation depth (stage 18: a subturn
// gets every tool EXCEPT run_subtask, so subturns cannot spawn their
// own subturns).
func (r *Registry) Without(names ...string) *Registry {
	ex := make(map[string]bool, len(names))
	for _, n := range names {
		ex[n] = true
	}
	out := &Registry{byName: make(map[string]Tool, len(r.tools))}
	for _, t := range r.tools {
		if ex[t.Name()] {
			continue
		}
		out.tools = append(out.tools, t)
		out.byName[t.Name()] = t
	}
	return out
}

// Only returns a new registry containing only the named tools. Unknown names
// grant nothing. Execute uses this registry too, so an unadvertised call cannot
// bypass the boundary. The original registry is unchanged.
func (r *Registry) Only(names ...string) *Registry {
	out := NewRegistry()
	for _, name := range names {
		if t, ok := r.byName[name]; ok {
			out.Add(t)
		}
	}
	return out
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

// Schema returns the parameters JSON Schema of the named tool, so the
// agent loop can repair mistyped arguments before dispatch (stage 14).
func (r *Registry) Schema(name string) (json.RawMessage, bool) {
	t, ok := r.byName[name]
	if !ok {
		return nil, false
	}
	p := t.Parameters()
	if len(p) == 0 {
		p = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return p, true
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
