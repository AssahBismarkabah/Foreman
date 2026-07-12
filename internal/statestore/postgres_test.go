package statestore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testDSN returns the PostgreSQL connection string for integration tests.
// Defaults to a local foreman_test database.  Override via FOREMAN_TEST_DATABASE_URL.
func testDSN() string {
	if dsn := os.Getenv("FOREMAN_TEST_DATABASE_URL"); dsn != "" {
		return dsn
	}
	return "postgres://foreman:foreman@localhost:5432/foreman_test?sslmode=disable"
}

// skipNoPostgres skips the test if PostgreSQL is unreachable.
// It also auto-creates the test database if it doesn't exist.
func skipNoPostgres(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Auto-create the test database if it doesn't exist.
	if err := EnsureDatabase(ctx, testDSN()); err != nil {
		t.Skipf("PostgreSQL not available (EnsureDatabase: %v)", err)
	}

	// Verify we can connect to the test database.
	dsn := testDSN()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("PostgreSQL not available: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Skipf("PostgreSQL ping failed: %v", err)
	}
}

// cleanupSessions removes all rows from the sessions table after a test.
func cleanupSessions(t *testing.T, store *postgresStore) {
	t.Helper()
	ctx := context.Background()
	_, err := store.pool.Exec(ctx, "DELETE FROM sessions")
	if err != nil {
		t.Logf("cleanup sessions: %v", err)
	}
}

func TestPostgresStore_CreateAndGetSession(t *testing.T) {
	skipNoPostgres(t)

	ctx := context.Background()
	store, err := NewPostgresStore(ctx, testDSN(), PoolConfig{})
	if err != nil {
		t.Fatalf("NewPostgresStore: %v", err)
	}
	defer store.Close()
	ps := store.(*postgresStore)
	defer cleanupSessions(t, ps)

	now := time.Now().UTC().Truncate(time.Millisecond)
	sess := Session{
		ID:        "ses_test_create_get",
		TaskID:    "task_1",
		UserID:    "user_abc",
		PluginID:  "slack",
		Status:    "CREATED",
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := store.GetSession(ctx, "ses_test_create_get")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}

	if got.ID != sess.ID {
		t.Errorf("ID = %q, want %q", got.ID, sess.ID)
	}
	if got.TaskID != sess.TaskID {
		t.Errorf("TaskID = %q, want %q", got.TaskID, sess.TaskID)
	}
	if got.UserID != sess.UserID {
		t.Errorf("UserID = %q, want %q", got.UserID, sess.UserID)
	}
	if got.PluginID != sess.PluginID {
		t.Errorf("PluginID = %q, want %q", got.PluginID, sess.PluginID)
	}
	if got.Status != sess.Status {
		t.Errorf("Status = %q, want %q", got.Status, sess.Status)
	}
}

func TestPostgresStore_UpdateSessionStatus(t *testing.T) {
	skipNoPostgres(t)

	ctx := context.Background()
	store, err := NewPostgresStore(ctx, testDSN(), PoolConfig{})
	if err != nil {
		t.Fatalf("NewPostgresStore: %v", err)
	}
	defer store.Close()
	ps := store.(*postgresStore)
	defer cleanupSessions(t, ps)

	now := time.Now().UTC().Truncate(time.Millisecond)
	sess := Session{
		ID:        "ses_test_update",
		TaskID:    "task_1",
		Status:    "CREATED",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	updatedAt := now.Add(5 * time.Second)
	if err := store.UpdateSessionStatus(ctx, "ses_test_update", "RUNNING", updatedAt); err != nil {
		t.Fatalf("UpdateSessionStatus: %v", err)
	}

	got, err := store.GetSession(ctx, "ses_test_update")
	if err != nil {
		t.Fatalf("GetSession after update: %v", err)
	}
	if got.Status != "RUNNING" {
		t.Errorf("Status = %q, want RUNNING", got.Status)
	}
	if !got.UpdatedAt.Equal(updatedAt) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, updatedAt)
	}
}

func TestPostgresStore_ListNonTerminalSessions(t *testing.T) {
	skipNoPostgres(t)

	ctx := context.Background()
	store, err := NewPostgresStore(ctx, testDSN(), PoolConfig{})
	if err != nil {
		t.Fatalf("NewPostgresStore: %v", err)
	}
	defer store.Close()
	ps := store.(*postgresStore)
	defer cleanupSessions(t, ps)

	now := time.Now().UTC().Truncate(time.Millisecond)

	// Insert 3 sessions: CREATED, COMPLETED, RUNNING
	sessions := []Session{
		{ID: "ses_nonterm_1", TaskID: "t1", Status: "CREATED", CreatedAt: now, UpdatedAt: now},
		{ID: "ses_term", TaskID: "t2", Status: "COMPLETED", CreatedAt: now, UpdatedAt: now},
		{ID: "ses_nonterm_2", TaskID: "t3", Status: "RUNNING", CreatedAt: now, UpdatedAt: now},
	}
	for _, s := range sessions {
		if err := store.CreateSession(ctx, s); err != nil {
			t.Fatalf("CreateSession(%s): %v", s.ID, err)
		}
	}

	nonTerm, err := store.ListNonTerminalSessions(ctx)
	if err != nil {
		t.Fatalf("ListNonTerminalSessions: %v", err)
	}

	if len(nonTerm) != 2 {
		t.Fatalf("expected 2 non-terminal sessions, got %d", len(nonTerm))
	}

	// Verify we got the non-terminal ones (order is created_at DESC)
	ids := make(map[string]bool)
	for _, s := range nonTerm {
		ids[s.ID] = true
	}
	if !ids["ses_nonterm_1"] {
		t.Error("missing ses_nonterm_1 (CREATED)")
	}
	if !ids["ses_nonterm_2"] {
		t.Error("missing ses_nonterm_2 (RUNNING)")
	}
	if ids["ses_term"] {
		t.Error("ses_term (COMPLETED) should not be listed")
	}
}

func TestPostgresStore_GetSessionNotFound(t *testing.T) {
	skipNoPostgres(t)

	ctx := context.Background()
	store, err := NewPostgresStore(ctx, testDSN(), PoolConfig{})
	if err != nil {
		t.Fatalf("NewPostgresStore: %v", err)
	}
	defer store.Close()

	_, err = store.GetSession(ctx, "ses_nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent session, got nil")
	}
}

func TestPostgresStore_AppendAuditEntry(t *testing.T) {
	skipNoPostgres(t)

	ctx := context.Background()
	store, err := NewPostgresStore(ctx, testDSN(), PoolConfig{})
	if err != nil {
		t.Fatalf("NewPostgresStore: %v", err)
	}
	defer store.Close()
	ps := store.(*postgresStore)
	defer cleanupSessions(t, ps)
	defer cleanupAuditLog(t, ps)

	// Need a session to exist for the FK constraint
	now := time.Now().UTC().Truncate(time.Millisecond)
	sess := Session{
		ID: "ses_audit_test", TaskID: "task_audit", Status: "CREATED",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	entry := AuditEntry{
		SessionID: "ses_audit_test",
		UserID:    "user_abc",
		Action:    "session.created",
		Target:    "ses_audit_test",
		Metadata:  map[string]any{"task_id": "task_audit"},
		CreatedAt: now,
	}
	if err := store.AppendAuditEntry(ctx, entry); err != nil {
		t.Fatalf("AppendAuditEntry: %v", err)
	}
}

func TestPostgresStore_AppendAuditEntryWithNilMetadata(t *testing.T) {
	skipNoPostgres(t)

	ctx := context.Background()
	store, err := NewPostgresStore(ctx, testDSN(), PoolConfig{})
	if err != nil {
		t.Fatalf("NewPostgresStore: %v", err)
	}
	defer store.Close()
	ps := store.(*postgresStore)
	defer cleanupSessions(t, ps)
	defer cleanupAuditLog(t, ps)

	now := time.Now().UTC().Truncate(time.Millisecond)
	sess := Session{
		ID: "ses_audit_nil", TaskID: "task_audit2", Status: "CREATED",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	entry := AuditEntry{
		SessionID: "ses_audit_nil",
		Action:    "session.completed",
		CreatedAt: now,
	}
	if err := store.AppendAuditEntry(ctx, entry); err != nil {
		t.Fatalf("AppendAuditEntry: %v", err)
	}
}

func cleanupAuditLog(t *testing.T, store *postgresStore) {
	t.Helper()
	ctx := context.Background()
	_, err := store.pool.Exec(ctx, "DELETE FROM audit_log")
	if err != nil {
		t.Logf("cleanup audit_log: %v", err)
	}
}

func TestPostgresStore_SaveAndGetCheckpoint(t *testing.T) {
	skipNoPostgres(t)

	ctx := context.Background()
	store, err := NewPostgresStore(ctx, testDSN(), PoolConfig{})
	if err != nil {
		t.Fatalf("NewPostgresStore: %v", err)
	}
	defer store.Close()
	ps := store.(*postgresStore)
	defer cleanupSessions(t, ps)
	defer cleanupCheckpoints(t, ps)

	now := time.Now().UTC().Truncate(time.Millisecond)
	sess := Session{
		ID: "ses_cp_test", TaskID: "task_cp", Status: "RUNNING",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	cp := Checkpoint{
		SessionID:  "ses_cp_test",
		Snapshot:   json.RawMessage(`{"files":["main.go"],"branch":"feature"}`),
		StepNumber: 1,
		CreatedAt:  now,
	}
	id, err := store.SaveCheckpoint(ctx, cp)
	if err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive checkpoint ID, got %d", id)
	}

	// Update checkpoint ref
	if err := store.UpdateCheckpointRef(ctx, "ses_cp_test", id); err != nil {
		t.Fatalf("UpdateCheckpointRef: %v", err)
	}

	// Verify checkpoint_ref on the session
	updated, err := store.GetSession(ctx, "ses_cp_test")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if updated.CheckpointRef != fmt.Sprintf("%d", id) {
		t.Errorf("CheckpointRef = %q, want %q", updated.CheckpointRef, fmt.Sprintf("%d", id))
	}

	// Retrieve last checkpoint
	last, err := store.LastCheckpoint(ctx, "ses_cp_test")
	if err != nil {
		t.Fatalf("LastCheckpoint: %v", err)
	}
	if last == nil {
		t.Fatal("expected checkpoint, got nil")
	}
	if last.SessionID != "ses_cp_test" {
		t.Errorf("SessionID = %q, want %q", last.SessionID, "ses_cp_test")
	}
	if last.StepNumber != 1 {
		t.Errorf("StepNumber = %d, want 1", last.StepNumber)
	}
}

func TestPostgresStore_CheckpointMultipleSteps(t *testing.T) {
	skipNoPostgres(t)

	ctx := context.Background()
	store, err := NewPostgresStore(ctx, testDSN(), PoolConfig{})
	if err != nil {
		t.Fatalf("NewPostgresStore: %v", err)
	}
	defer store.Close()
	ps := store.(*postgresStore)
	defer cleanupSessions(t, ps)
	defer cleanupCheckpoints(t, ps)

	now := time.Now().UTC().Truncate(time.Millisecond)
	sess := Session{
		ID: "ses_cp_multi", TaskID: "task_cp2", Status: "RUNNING",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Save checkpoints at steps 1, 2, 3
	for step := 1; step <= 3; step++ {
		cp := Checkpoint{
			SessionID:  "ses_cp_multi",
			Snapshot:   json.RawMessage(`{"step":` + fmt.Sprintf("%d", step) + `}`),
			StepNumber: step,
			CreatedAt:  now.Add(time.Duration(step) * time.Second),
		}
		id, err := store.SaveCheckpoint(ctx, cp)
		if err != nil {
			t.Fatalf("SaveCheckpoint step %d: %v", step, err)
		}
		if id <= 0 {
			t.Fatalf("expected positive ID at step %d", step)
		}
	}

	// Last should be step 3
	last, err := store.LastCheckpoint(ctx, "ses_cp_multi")
	if err != nil {
		t.Fatalf("LastCheckpoint: %v", err)
	}
	if last == nil {
		t.Fatal("expected checkpoint, got nil")
	}
	if last.StepNumber != 3 {
		t.Errorf("Expected step 3, got %d", last.StepNumber)
	}
}

func TestPostgresStore_LastCheckpointNonexistent(t *testing.T) {
	skipNoPostgres(t)

	ctx := context.Background()
	store, err := NewPostgresStore(ctx, testDSN(), PoolConfig{})
	if err != nil {
		t.Fatalf("NewPostgresStore: %v", err)
	}
	defer store.Close()

	last, err := store.LastCheckpoint(ctx, "ses_nonexistent")
	if err != nil {
		t.Fatalf("LastCheckpoint: %v", err)
	}
	if last != nil {
		t.Fatal("expected nil for nonexistent session, got checkpoint")
	}
}

func cleanupCheckpoints(t *testing.T, store *postgresStore) {
	t.Helper()
	ctx := context.Background()
	_, err := store.pool.Exec(ctx, "DELETE FROM checkpoints")
	if err != nil {
		t.Logf("cleanup checkpoints: %v", err)
	}
}

func TestPostgresStore_Ping(t *testing.T) {
	skipNoPostgres(t)

	ctx := context.Background()
	store, err := NewPostgresStore(ctx, testDSN(), PoolConfig{})
	if err != nil {
		t.Fatalf("NewPostgresStore: %v", err)
	}
	defer store.Close()

	if err := store.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}
