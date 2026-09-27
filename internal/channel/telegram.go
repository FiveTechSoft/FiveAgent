package channel

import "context"

// telegram is the Telegram Bot API adapter (long polling). Skeleton.
type telegram struct {
	token string
	core  Handler
}

// NewTelegram builds the Telegram adapter.
func NewTelegram(token string, core Handler) Channel {
	return &telegram{token: token, core: core}
}

func (t *telegram) Name() string { return "telegram" }

// Run polls getUpdates and feeds messages into the agent. Skeleton:
// real implementation lands in the v0 milestone.
func (t *telegram) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}
