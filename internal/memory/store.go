// Package memory keeps conversation history and long-term recall in Postgres
// (pgvector for embeddings). The user's data never leaves their machine.
package memory

import (
	"context"
	"database/sql"

	_ "github.com/lib/pq"
)

// Store is the Postgres-backed memory.
type Store struct {
	db *sql.DB
}

// Open connects and runs migrations.
func Open(dsn string) (*Store, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	s := &Store{db: db}
	return s, s.migrate()
}

// Close closes the database handle.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id BIGSERIAL PRIMARY KEY,
			channel TEXT NOT NULL,
			user_id TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
	return err
}

// Append records one message.
func (s *Store) Append(ctx context.Context, channel, userID, role, content string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (channel, user_id, role, content) VALUES ($1, $2, $3, $4)`,
		channel, userID, role, content)
	return err
}

// Recent returns the latest messages for a user, oldest first.
func (s *Store) Recent(ctx context.Context, channel, userID string, limit int) ([][2]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT role, content FROM (
			SELECT role, content, id FROM messages
			WHERE channel = $1 AND user_id = $2
			ORDER BY id DESC LIMIT $3
		) m ORDER BY id`, channel, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var m [2]string
		if err := rows.Scan(&m[0], &m[1]); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
