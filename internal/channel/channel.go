// Package channel normalizes messaging platforms into one event shape.
package channel

import (
	"context"
	"log"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
)

// agentBudget caps a whole agent run (model call plus tool rounds)
// from the model timeout: three model calls worst case, plus margin.
func agentBudget(cfg *config.Config) time.Duration {
	return 3*model.ModelTimeout(cfg.Model) + 30*time.Second
}

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

// senderAllowed reports whether id may talk to the bot. An empty list
// allows everyone.
func senderAllowed(list []string, id string) bool {
	if len(list) == 0 {
		return true
	}
	for _, a := range list {
		if a == id {
			return true
		}
	}
	return false
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
	// Stage 19: one ledger shared by every adapter. A corrupt or
	// unreadable file disables the ledger, never the channels.
	ledgerPath := cfg.Delivery.Path
	if ledgerPath == "" {
		ledgerPath = "data/deliveries.json"
	}
	ledger, err := OpenLedger(ledgerPath)
	if err != nil {
		log.Printf("delivery ledger disabled: %v", err)
		ledger = nil
	}
	var out []Channel
	for name, ch := range cfg.Channels {
		if !ch.Enabled {
			continue
		}
		switch name {
		case "telegram":
			t := NewTelegram(ch, core).(*telegram)
			t.agentTimeout = agentBudget(cfg)
			t.ledger = ledger
			out = append(out, t)
		case "whatsapp":
			w := NewWhatsApp(ch, core).(*whatsapp)
			w.agentTimeout = agentBudget(cfg)
			w.ledger = ledger
			out = append(out, w)
		default:
			out = append(out, &noop{name: name})
		}
	}
	return out
}
