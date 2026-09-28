package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/sandbox"
)

// ctxKey carries the current request's channel and user so tools can
// scope their work (the sandbox keeps one writable folder per user).
type ctxKey struct{}

// WithRequestInfo returns a ctx carrying the channel and user ID of the
// message being answered. The agent sets it before calling tools.
func WithRequestInfo(ctx context.Context, channel, userID string) context.Context {
	return context.WithValue(ctx, ctxKey{}, channel+"/"+userID)
}

// RunCommand lets the model execute commands inside the per-user
// sandbox: no network, only the user's own folder writable, killed
// after the configured timeout.
type RunCommand struct {
	SB sandbox.Sandbox
}

// Name implements Tool.
func (RunCommand) Name() string { return "run_command" }

// Description implements Tool.
func (r RunCommand) Description() string {
	limits := "no network, only the user's own folder is writable, killed after a timeout"
	if r.SB.Name() == "jobobject" {
		limits = "runs in the user's own folder with a RAM cap and a timeout; filesystem and network isolation are not enforced yet on Windows"
	}
	return fmt.Sprintf("Run a command inside the user's sandbox (%s backend): %s. Good for calculations, small scripts and file tasks. Files persist between calls.", r.SB.Name(), limits)
}

// Parameters implements Tool.
func (RunCommand) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {
				"type": "string",
				"description": "The program to run, e.g. python3 or cmd"
			},
			"args": {
				"type": "array",
				"items": {"type": "string"},
				"description": "Arguments, e.g. [\"-c\", \"print(2+2)\"]"
			}
		},
		"required": ["command"]
	}`)
}

// Execute implements Tool.
func (r RunCommand) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	userKey, _ := ctx.Value(ctxKey{}).(string)
	if userKey == "" {
		return "", fmt.Errorf("run_command: no user context")
	}
	var a struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	if strings.TrimSpace(a.Command) == "" {
		return "", fmt.Errorf("run_command: empty command")
	}
	start := time.Now()
	res, err := r.SB.Run(ctx, userKey, append([]string{a.Command}, a.Args...))
	if err != nil {
		// The audit trail distinguishes a real execution from a model
		// that claims it ran something (first-live-test finding).
		log.Printf("run_command audit user=%s cmd=%q args=%q error=%v", userKey, a.Command, a.Args, err)
		return "", err
	}
	log.Printf("run_command audit user=%s cmd=%q args=%q exit=%d dur=%s stderr=%.160q", userKey, a.Command, a.Args, res.ExitCode, time.Since(start).Round(time.Millisecond), res.Stderr)
	var sb strings.Builder
	if res.Stdout != "" {
		sb.WriteString(res.Stdout)
	}
	if res.Stderr != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("stderr: " + res.Stderr)
	}
	if res.TimedOut {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("(killed: timeout)")
	}
	out := sb.String()
	const maxForModel = 4000
	if len(out) > maxForModel {
		out = out[:maxForModel] + "\n(truncated)"
	}
	if out == "" {
		out = fmt.Sprintf("(no output, exit code %d)", res.ExitCode)
	}
	return out, nil
}
