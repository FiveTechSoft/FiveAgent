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
	return fmt.Sprintf("%s You run on the model %s via %s; if asked, say so plainly. FiveAgent works with any OpenAI-compatible provider (DeepSeek, Ollama, OpenAI, ...): your owner can switch the model by editing fiveagent.yml, so never claim you cannot use one of them. FiveAgent is free and open source under the MIT license; its repo is https://github.com/FiveTechSoft/FiveAgent.", p, cfg.Model.Name, host)
}

// channelStyle tells the model how the channel renders text, so it does
// not emit Markdown the channel would show literally.
func channelStyle(channel string) string {
	switch channel {
	case "whatsapp":
		return "Formatting for WhatsApp: *bold*, _italic_, ~strikethrough~ and ```code``` only; no Markdown headers, links or tables (they show literally)."
	case "telegram":
		// sendMessage is sent without parse_mode: Telegram renders plain text.
		return "Formatting for Telegram: plain text only; Markdown is not rendered and would show literally."
	default:
		return "Reply in plain text; do not rely on Markdown formatting."
	}
}

// Agent ties the model, memory and tools together.
type Agent struct {
	mdl       *model.Client
	coder     *model.Client // optional: serves code-heavy requests
	store     memory.Store
	tools     *tools.Registry
	sysPrompt string
}

// New builds the core. sysPrompt comes from SystemPrompt(cfg).
func New(mdl *model.Client, store memory.Store, reg *tools.Registry, sysPrompt string) *Agent {
	return &Agent{mdl: mdl, store: store, tools: reg, sysPrompt: sysPrompt}
}

// WithCoder sets the optional second model for code-heavy requests and
// returns the agent for chaining.
func (a *Agent) WithCoder(c *model.Client) *Agent {
	a.coder = c
	return a
}

// looksLikeCode reports whether a message is code-heavy enough to be
// served by the coder model. It is a cheap heuristic on the user text:
// code fences, structural symbols and programming keywords in English
// and Spanish. False positives only cost speed (the coder model is
// usually the bigger one), so the bar favors precision over recall.
func looksLikeCode(s string) bool {
	if strings.Contains(s, "```") {
		return true
	}
	low := strings.ToLower(s)
	for _, sig := range []string{
		"func ", "def ", "function(", "function (", "class ", "import ",
		"=>", "console.log", "#include", "const ",
		"{{", "}}", "; ", "git status", "git commit", "git push", "git pull",
		"traceback", "stacktrace", "compile", "compiler", "regex",
		"select ", "from where", "python", "javascript", "typescript", "golang", "c++",
		"código", "codigo", "función", "algoritmo",
		"compilar", "depurar", "bug", "sql",
	} {
		if strings.Contains(low, sig) {
			return true
		}
	}
	return false
}

// Handle answers one inbound message, keeping history per channel+user.
// Tool-call iterations stay in memory; only the user text and the final
// reply are persisted.
func (a *Agent) Handle(ctx context.Context, channel, userID, text string) (string, error) {
	// Tools (e.g. the sandbox) scope their work per channel+user.
	ctx = tools.WithRequestInfo(ctx, channel, userID)
	if err := a.store.Append(ctx, channel, userID, "user", text); err != nil {
		return "", err
	}
	history, err := a.store.Recent(ctx, channel, userID, 20)
	if err != nil {
		return "", err
	}
	msgs := []model.Message{{Role: "system", Content: a.sysPrompt + " " + channelStyle(channel)}}
	for _, h := range history {
		// Never replay a stored system message: the prompt comes from
		// the current code/config, so upgrades take effect at once and
		// an old prompt lingering in memory cannot override it.
		if h[0] == "system" {
			continue
		}
		msgs = append(msgs, model.Message{Role: h[0], Content: h[1]})
	}

	var reply string
	// The agent decides which model serves this request: the coder model
	// for code-heavy text, the main model otherwise.
	mdl := a.mdl
	if a.coder != nil && looksLikeCode(text) {
		mdl = a.coder
	}
	for round := 0; round < maxToolRounds; round++ {
		ans, err := mdl.Chat(ctx, msgs, a.tools.Specs())
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
