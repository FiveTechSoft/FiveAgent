// Package agent is the core loop: context in, model call, tool calls, reply out.
package agent

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/memory"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

// maxToolRounds caps model-tool round trips per user message.
const maxToolRounds = 5

// baseSystemPrompt is the built-in persona, used when fiveagent.yml sets
// no system_prompt.
const baseSystemPrompt = "You are FiveAgent, a helpful personal assistant. Be concise and warm."

// SystemPrompt builds the system prompt for the model: the configured
// persona (or the built-in one) plus one line naming the configured model,
// so the agent can say plainly what it runs on instead of inventing an
// identity.
func SystemPrompt(cfg *config.Config) string {
	p := strings.TrimSpace(cfg.SystemPrompt)
	if p == "" {
		p = baseSystemPrompt
	}
	host := cfg.Model.BaseURL
	if u, err := url.Parse(cfg.Model.BaseURL); err == nil && u.Host != "" {
		host = u.Host
	}
	return fmt.Sprintf("%s You run on the model %s via %s; if asked, say so plainly.", p, cfg.Model.Name, host)
}

// Agent ties the model, memory and tools together.
type Agent struct {
	mdl       *model.Client
	store     memory.Store
	tools     *tools.Registry
	sysPrompt string
}

// New builds the core. sysPrompt comes from SystemPrompt(cfg).
func New(mdl *model.Client, store memory.Store, reg *tools.Registry, sysPrompt string) *Agent {
	return &Agent{mdl: mdl, store: store, tools: reg, sysPrompt: sysPrompt}
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
	msgs := []model.Message{{Role: "system", Content: a.sysPrompt}}
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
