// Package statestore provides durable persistence for Foreman sessions and
// related entities.  The primary implementation uses PostgreSQL via pgx/v5.
//
// # Design decisions
//
//   - PostgreSQL is the single source of truth (see architecture.md appendix B).
//   - pgx/v5 native interface (not database/sql) for performance and full
//     PostgreSQL feature access (binary protocol, COPY, LISTEN/NOTIFY).
//   - Schema migrations use golang-migrate with embedded SQL files.
//   - The interface is session-centric.  Future migrations will add tables for
//     checkpoints, audit log, identity bindings, and policies.
package statestore

import (
	"context"
	"time"
)

// Session is the durable representation of a Foreman session.
// It mirrors controlplane.Session without the in-memory mutex.
type Session struct {
	ID            string    `json:"id"`
	TaskID        string    `json:"task_id"`
	UserID        string    `json:"user_id,omitempty"`
	PluginID      string    `json:"plugin_id,omitempty"`
	Status        string    `json:"status"` // schemas.SessionStatus as string
	Description   string    `json:"description,omitempty"`
	CheckpointRef string    `json:"checkpoint_ref,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// StateStore is the persistence contract for Foreman.
// Implementations must be safe for concurrent use.
type StateStore interface {
	// CreateSession inserts a new session row.  Returns an error if the
	// session ID already exists.
	CreateSession(ctx context.Context, s Session) error

	// GetSession retrieves a single session by ID.
	GetSession(ctx context.Context, id string) (Session, error)

	// UpdateSessionStatus updates the status and updated_at timestamp for
	// a session.  This is the most frequent write operation.
	UpdateSessionStatus(ctx context.Context, id string, status string, updatedAt time.Time) error

	// ListNonTerminalSessions returns all sessions whose status is not
	// COMPLETED or FAILED.
	ListNonTerminalSessions(ctx context.Context) ([]Session, error)

	// Ping verifies the database connection is alive.
	Ping(ctx context.Context) error

	// Close releases all resources held by the store.
	Close()
}
