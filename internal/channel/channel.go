// Package channel normalizes messaging platforms into one event shape.
package channel

import (
	"context"
	"log"

	"github.com/FiveTechSoft/FiveAgent/internal/config"
)

// Event is one inbound message, whatever the platform.
type Event struct {
	Channel string // "telegram", "whatsapp", "imessage"
	UserID  string
	Text    string
	Reply   func(ctx context.Context, text string) error
}

// Handler is what channels need from the core: answer one message.
// *agent.Agent satisfies it; tests can use a fake.
type Handler interface {
	Handle(ctx context.Context, channel, userID, text string) (string, error)
}

// Channel is one messaging adapter.
type Channel interface {
	Name() string
	// Run starts listening and blocks until ctx is cancelled.
	Run(ctx context.Context) error
}

// noop is a placeholder adapter for channels not yet implemented.
type noop struct{ name string }

func (n *noop) Name() string { return n.name }
func (n *noop) Run(ctx context.Context) error {
	log.Printf("channel %s: adapter not implemented yet, idling", n.name)
	<-ctx.Done()
	return ctx.Err()
}

// Build returns the adapters enabled in config.
func Build(cfg *config.Config, core Handler) []Channel {
	var out []Channel
	for name, ch := range cfg.Channels {
		if !ch.Enabled {
			continue
		}
		switch name {
		case "telegram":
			out = append(out, NewTelegram(ch.BotToken, core))
		case "whatsapp":
			out = append(out, NewWhatsApp(ch, core))
		default:
			out = append(out, &noop{name: name})
		}
	}
	return out
}
