// Package tools holds the agent's tools: web search, files, email, calendar
// and the sandboxed browser (Playwright sidecar). Skeleton for v0.
package tools

// Tool is one capability the agent can invoke.
type Tool interface {
	Name() string
	Description() string
}
