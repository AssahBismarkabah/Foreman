package statestore

import (
	"context"
	"embed"
	"fmt"
	"log"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// PoolConfig holds tuning parameters for the pgx connection pool.
// Zero values leave pgxpool defaults in place.
type PoolConfig struct {
	MaxConns        int32         `yaml:"max_connections"`
	MinConns        int32         `yaml:"min_connections"`
	MaxConnLifetime time.Duration `yaml:"max_conn_lifetime"`
	MaxConnIdleTime time.Duration `yaml:"max_conn_idle_time"`
}

// postgresStore implements StateStore backed by a pgxpool.
type postgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore creates a StateStore, runs pending migrations, and returns
// a ready-to-use store.  Callers must call Close() when done.
func NewPostgresStore(ctx context.Context, dsn string, cfg PoolConfig) (StateStore, error) {
	// Ensure the target database exists before connecting.
	if err := EnsureDatabase(ctx, dsn); err != nil {
		return nil, fmt.Errorf("statestore: %w", err)
	}

	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("statestore: parse dsn: %w", err)
	}

	if cfg.MaxConns > 0 {
		poolCfg.MaxConns = cfg.MaxConns
	}
	if cfg.MinConns > 0 {
		poolCfg.MinConns = cfg.MinConns
	}
	if cfg.MaxConnLifetime > 0 {
		poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	}
	if cfg.MaxConnIdleTime > 0 {
		poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	}
	// Always health-check idle connections periodically.
	poolCfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("statestore: create pool: %w", err)
	}

	// Verify the connection is live before proceeding.
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("statestore: ping: %w", err)
	}

	// Run schema migrations.
	if err := runMigrations(ctx, dsn); err != nil {
		pool.Close()
		return nil, fmt.Errorf("statestore: migrations: %w", err)
	}

	return &postgresStore{pool: pool}, nil
}

// EnsureDatabase creates the database specified in the DSN if it doesn't exist.
// It connects to the default 'postgres' admin database to check and create.
// Idempotent -- safe to call repeatedly.
func EnsureDatabase(ctx context.Context, dsn string) error {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("ensure database: parse dsn: %w", err)
	}

	targetDB := cfg.ConnConfig.Database
	if targetDB == "" || targetDB == "postgres" {
		return nil // no custom database to ensure
	}

	// Connect to the admin database to run admin-level DDL (CREATE DATABASE
	// cannot run inside a transaction, so we use a separate connection).
	cfg.ConnConfig.Database = "postgres"
	adminPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("ensure database: connect to admin: %w", err)
	}
	defer adminPool.Close()

	var exists bool
	if err := adminPool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", targetDB,
	).Scan(&exists); err != nil {
		return fmt.Errorf("ensure database: check existence: %w", err)
	}

	if !exists {
		quoted := pgx.Identifier{targetDB}.Sanitize()
		if _, err := adminPool.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
			return fmt.Errorf("ensure database: create %s: %w", targetDB, err)
		}
	}

	return nil
}

// --- StateStore implementation ----------------------------------------------

func (s *postgresStore) CreateSession(ctx context.Context, sess Session) error {
	const q = `
		INSERT INTO sessions (id, task_id, user_id, plugin_id, status, checkpoint_ref, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	_, err := s.pool.Exec(ctx, q,
		sess.ID, sess.TaskID, nullString(sess.UserID), nullString(sess.PluginID),
		sess.Status, nullString(sess.CheckpointRef),
		sess.CreatedAt, sess.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (s *postgresStore) GetSession(ctx context.Context, id string) (Session, error) {
	const q = `
		SELECT id, task_id, COALESCE(user_id, ''), COALESCE(plugin_id, ''), status,
		       COALESCE(checkpoint_ref, ''), created_at, updated_at
		FROM sessions WHERE id = $1
	`
	row := s.pool.QueryRow(ctx, q, id)
	var sess Session
	err := row.Scan(
		&sess.ID, &sess.TaskID, &sess.UserID, &sess.PluginID,
		&sess.Status, &sess.CheckpointRef,
		&sess.CreatedAt, &sess.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return sess, fmt.Errorf("session %q not found", id)
	}
	if err != nil {
		return sess, fmt.Errorf("get session: %w", err)
	}
	return sess, nil
}

func (s *postgresStore) UpdateSessionStatus(ctx context.Context, id string, status string, updatedAt time.Time) error {
	const q = `UPDATE sessions SET status = $2, updated_at = $3 WHERE id = $1`
	tag, err := s.pool.Exec(ctx, q, id, status, updatedAt)
	if err != nil {
		return fmt.Errorf("update session status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("session %q not found", id)
	}
	return nil
}

func (s *postgresStore) ListNonTerminalSessions(ctx context.Context) ([]Session, error) {
	const q = `
		SELECT id, task_id, COALESCE(user_id, ''), COALESCE(plugin_id, ''), status,
		       COALESCE(checkpoint_ref, ''), created_at, updated_at
		FROM sessions
		WHERE status NOT IN ('COMPLETED', 'FAILED')
		ORDER BY created_at DESC
	`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list non-terminal sessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var sess Session
		if err := rows.Scan(
			&sess.ID, &sess.TaskID, &sess.UserID, &sess.PluginID,
			&sess.Status, &sess.CheckpointRef,
			&sess.CreatedAt, &sess.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		sessions = append(sessions, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration: %w", err)
	}
	// Return empty slice instead of nil for JSON serialization consistency.
	if sessions == nil {
		sessions = []Session{}
	}
	return sessions, nil
}

func (s *postgresStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *postgresStore) Close() {
	s.pool.Close()
}

// --- helpers ----------------------------------------------------------------

// runMigrations applies all pending SQL migration files to the database using
// golang-migrate with an embedded filesystem source.
func runMigrations(ctx context.Context, dsn string) error {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", src, dsn)
	if err != nil {
		return fmt.Errorf("init migration: %w", err)
	}
	defer func() {
		if sErr, dErr := m.Close(); sErr != nil || dErr != nil {
			log.Printf("statestore: close migration: source=%v, database=%v", sErr, dErr)
		}
	}()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

// nullString returns a *string for pgx nullable columns.
// pgx treats "" as a valid value, so we use nil for empty strings
// when the column is nullable.
func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
