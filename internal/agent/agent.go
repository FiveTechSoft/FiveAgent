// Package agent is the core loop: context in, model call, tool calls, reply out.
package agent

import (
	"context"
	"fmt"

	"github.com/FiveTechSoft/FiveAgent/internal/memory"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

// maxToolRounds caps model-tool round trips per user message.
const maxToolRounds = 5

const systemPrompt = "You are FiveAgent, a helpful personal assistant. Be concise and warm."

// Agent ties the model, memory and tools together.
type Agent struct {
	mdl   *model.Client
	store memory.Store
	tools *tools.Registry
}

// New builds the core.
func New(mdl *model.Client, store memory.Store, reg *tools.Registry) *Agent {
	return &Agent{mdl: mdl, store: store, tools: reg}
}

// Handle answers one inbound message, keeping history per channel+user.
// Tool-call iterations stay in memory; only the user text and the final
// reply are persisted.
func (a *Agent) Handle(ctx context.Context, channel, userID, text string) (string, error) {
	if err := a.store.Append(ctx, channel, userID, "user", text); err != nil {
		return "", err
	}
	history, err := a.store.Recent(ctx, channel, userID, 20)
	if err != nil {
		return "", err
	}
	msgs := []model.Message{{Role: "system", Content: systemPrompt}}
	for _, h := range history {
		msgs = append(msgs, model.Message{Role: h[0], Content: h[1]})
	}

	var reply string
	for round := 0; round < maxToolRounds; round++ {
		ans, err := a.mdl.Chat(ctx, msgs, a.tools.Specs())
		if err != nil {
			return "", err
		}
		if len(ans.ToolCalls) == 0 {
			reply = ans.Content
			break
		}
		msgs = append(msgs, ans)
		for _, call := range ans.ToolCalls {
			result, err := a.tools.Execute(ctx, call.Function.Name, []byte(call.Function.Arguments))
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
		return "", fmt.Errorf("agent: no final answer after %d tool rounds", maxToolRounds)
	}
	if err := a.store.Append(ctx, channel, userID, "assistant", reply); err != nil {
		return "", err
	}
	return reply, nil
}
