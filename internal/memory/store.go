// Package memory keeps conversation history and long-term recall: a JSON file
// for local prototypes or Postgres for real deployments. The user's data never
// leaves their machine.
package memory

import "context"

// Store is one memory backend: append-only history keyed by channel+user.
type Store interface {
	// Append records one message.
	Append(ctx context.Context, channel, userID, role, content string) error
	// Recent returns the latest messages for a user, oldest first.
	Recent(ctx context.Context, channel, userID string, limit int) ([][2]string, error)
	// Scrub redacts the given words out of every stored message of one
	// conversation: an "olvida:" must reach the live context too, or the
	// model simply quotes the forgotten fact back from history (battery
	// run 9: "pulpo" cited from a save_memory turn long after the
	// forget). It returns how many messages were rewritten.
	Scrub(ctx context.Context, channel, userID string, words []string) (int, error)
	// Close releases the underlying resources.
	Close() error
}
