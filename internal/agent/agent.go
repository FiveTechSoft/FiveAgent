// Package agent is the core loop: context in, model call, reply out.
package agent

import (
	"context"

	"github.com/FiveTechSoft/FiveAgent/internal/memory"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
)

const systemPrompt = "You are FiveAgent, a helpful personal assistant. Be concise and warm."

// Agent ties the model and memory together.
type Agent struct {
	mdl   *model.Client
	store *memory.Store
}

// New builds the core.
func New(mdl *model.Client, store *memory.Store) *Agent {
	return &Agent{mdl: mdl, store: store}
}

// Handle answers one inbound message, keeping history per channel+user.
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
	reply, err := a.mdl.Chat(ctx, msgs)
	if err != nil {
		return "", err
	}
	if err := a.store.Append(ctx, channel, userID, "assistant", reply); err != nil {
		return "", err
	}
	return reply, nil
}
