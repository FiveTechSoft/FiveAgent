// subtask.go - subordinate agents (stage 18 of docs/ROADMAP.md).
//
// A big task fails as one turn of a small model but succeeds split
// into subtasks that each fit one clean turn. The model drives the
// split itself through the run_subtask tool: each call runs ONE
// subtask in an isolated subturn - fresh context (no conversation
// history, no memory recall, no store writes), the same model and
// a fixed set of query tools, so delegation is capped
// at depth 1. run_subtask is sequential; its sibling run_subtasks
// (stage 34) fans independent subtasks out in parallel. The subturn
// result comes back as the tool result and the main turn composes
// the final answer.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

// subordinatePrompt is the fresh system prompt of a subturn: one
// subtask, no history, answer directly.
const subordinatePrompt = "You are a helper inside FiveAgent solving ONE small subtask with a fresh context. You see no conversation history: everything you know is in the subtask text. You can use only query tools; leave sends, writes, commands and browser actions to the main turn. Answer the subtask directly and concisely, using the available tools when the subtask needs them. Honesty above fluency: if you cannot know, say so."

// subtaskTool is the run_subtask tool the agent registers on itself.
type subtaskTool struct {
	a *Agent
}

// Name implements tools.Tool.
func (subtaskTool) Name() string { return "run_subtask" }

// Description implements tools.Tool.
func (subtaskTool) Description() string {
	return "Run ONE small subtask in an isolated helper turn with a fresh context (no conversation history) and get its result. Use for multi-step requests: split the work into steps that each fit one clean turn, run them one at a time with this tool, then compose the final answer from the results."
}

// Parameters implements tools.Tool.
func (subtaskTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"task": {
				"type": "string",
				"description": "The complete, self-contained text of the subtask to solve."
			}
		},
		"required": ["task"]
	}`)
}

// Execute implements tools.Tool.
func (t subtaskTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Task string `json:"task"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("run_subtask: bad arguments: %w", err)
	}
	task := strings.TrimSpace(a.Task)
	if task == "" {
		return "", fmt.Errorf("run_subtask: empty task")
	}
	return t.a.runSubTurn(ctx, task)
}

// runSubTurn runs one subtask in an isolated subturn and returns its
// reply. Fresh context by design: no store reads or writes, no memory
// recall, no skills, no pruning (a fresh turn is short). The subturn
// gets only known query tools; actions remain with the main turn.
func (a *Agent) runSubTurn(ctx context.Context, task string) (string, error) {
	channel, _ := tools.RequestInfo(ctx)
	msgs := []model.Message{
		{Role: "system", Content: subordinatePrompt + " " + channelStyle(channel)},
		{Role: "user", Content: task},
	}
	mdl, fallback := a.mdl, a.coder
	if a.coder != nil && looksLikeCode(task) {
		mdl, fallback = a.coder, a.mdl
	}
	// No model-supplied task can widen this allowlist. In particular, helpers
	// cannot send, mutate memory, run commands, act in the shared browser or
	// spawn more helpers. New tools stay unavailable until reviewed here.
	reg := a.tools.Only(
		"current_datetime", "web_search", "duckduckgo", "brave", "read_file",
		"gmail_search", "calendar_list", "drive_list", "drive_download",
		"slack_channels", "slack_read", "github_repos", "github_issues",
	)
	specs := reg.Specs()
	var reply string
	for round := 0; round < maxToolRounds; round++ {
		ans, err := a.recoverableChat(ctx, mdl, fallback, msgs, specs, a.toolCallOptions())
		if err != nil {
			return "", err
		}
		if len(ans.ToolCalls) == 0 {
			reply = strings.TrimSpace(ans.Content)
			break // recoverableChat already ran the empty-reply ladder
		}
		msgs = append(msgs, ans)
		for _, call := range ans.ToolCalls {
			rawArgs := []byte(call.Function.Arguments)
			if schema, ok := reg.Schema(call.Function.Name); ok {
				if fixed, repairs, rerr := tools.RepairArgs(schema, rawArgs); rerr == nil && len(repairs) > 0 {
					log.Printf("agent subturn tool repair %s: %s", call.Function.Name, strings.Join(repairs, "; "))
					rawArgs = fixed
				}
			}
			result, err := reg.Execute(ctx, call.Function.Name, rawArgs)
			if err != nil {
				result = fmt.Sprintf("error: %v", err)
			}
			msgs = append(msgs, model.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: call.ID,
			})
		}
	}
	if reply == "" {
		return "", fmt.Errorf("run_subtask: the subturn returned empty after %d rounds", maxToolRounds)
	}
	log.Printf("agent: subtask %.60q -> %d chars", task, len(reply))
	return reply, nil
}
