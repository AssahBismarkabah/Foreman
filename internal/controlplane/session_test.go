package controlplane

import (
	"context"
	"testing"

	"github.com/foreman/foreman/internal/eventbus"
	"github.com/foreman/foreman/internal/schemas"
)

func newTestCP(t *testing.T) (*ControlPlane, context.Context) {
	t.Helper()
	bus := eventbus.NewMemoryBus()
	cp := New(bus)
	return cp, context.Background()
}

func TestCreateSession(t *testing.T) {
	cp, ctx := newTestCP(t)
	err := cp.CreateSession(ctx, "ses_1", "task_1")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	s, ok := cp.GetSession("ses_1")
	if !ok {
		t.Fatal("session not found after CreateSession")
	}
	if s.Status != schemas.StatusCreated {
		t.Errorf("expected status CREATED, got %s", s.Status)
	}
	if s.TaskID != "task_1" {
		t.Errorf("expected TaskID task_1, got %s", s.TaskID)
	}
}

func TestDuplicateCreate(t *testing.T) {
	cp, ctx := newTestCP(t)
	if err := cp.CreateSession(ctx, "ses_1", "t1"); err != nil {
		t.Fatal(err)
	}
	// Creating with same ID overwrites (map assignment) -- not a bug, just
	// a design choice. The second creation silently replaces the first.
	// This test documents the current behaviour.
	if err := cp.CreateSession(ctx, "ses_1", "t2"); err != nil {
		t.Fatal(err)
	}
	s, ok := cp.GetSession("ses_1")
	if !ok {
		t.Fatal("session not found")
	}
	if s.TaskID != "t2" {
		t.Errorf("expected TaskID t2 (overwritten), got %s", s.TaskID)
	}
}

func TestGetSessionNotFound(t *testing.T) {
	cp, _ := newTestCP(t)
	_, ok := cp.GetSession("nonexistent")
	if ok {
		t.Fatal("expected false for nonexistent session")
	}
}

func TestValidTransitions(t *testing.T) {
	tests := []struct {
		name  string
		from  schemas.SessionStatus
		to    schemas.SessionStatus
		valid bool
	}{
		{"created->allocating", schemas.StatusCreated, schemas.StatusAllocating, true},
		{"created->failed", schemas.StatusCreated, schemas.StatusFailed, true},
		{"created->running", schemas.StatusCreated, schemas.StatusRunning, false},
		{"created->completed", schemas.StatusCreated, schemas.StatusCompleted, false},
		{"created->cancelling", schemas.StatusCreated, schemas.StatusCancelling, false},
		{"created->approval", schemas.StatusCreated, schemas.StatusApproval, false},

		{"allocating->running", schemas.StatusAllocating, schemas.StatusRunning, true},
		{"allocating->failed", schemas.StatusAllocating, schemas.StatusFailed, true},
		{"allocating->completed", schemas.StatusAllocating, schemas.StatusCompleted, false},
		{"allocating->created", schemas.StatusAllocating, schemas.StatusCreated, false},

		{"running->approval", schemas.StatusRunning, schemas.StatusApproval, true},
		{"running->completed", schemas.StatusRunning, schemas.StatusCompleted, true},
		{"running->cancelling", schemas.StatusRunning, schemas.StatusCancelling, true},
		{"running->failed", schemas.StatusRunning, schemas.StatusFailed, true},
		{"running->allocating", schemas.StatusRunning, schemas.StatusAllocating, false},
		{"running->created", schemas.StatusRunning, schemas.StatusCreated, false},

		{"approval->running", schemas.StatusApproval, schemas.StatusRunning, true},
		{"approval->cancelling", schemas.StatusApproval, schemas.StatusCancelling, true},
		{"approval->failed", schemas.StatusApproval, schemas.StatusFailed, true},
		{"approval->completed", schemas.StatusApproval, schemas.StatusCompleted, false},
		{"approval->allocating", schemas.StatusApproval, schemas.StatusAllocating, false},

		{"cancelling->completed", schemas.StatusCancelling, schemas.StatusCompleted, true},
		{"cancelling->failed", schemas.StatusCancelling, schemas.StatusFailed, true},
		{"cancelling->running", schemas.StatusCancelling, schemas.StatusRunning, false},
		{"cancelling->allocating", schemas.StatusCancelling, schemas.StatusAllocating, false},

		{"completed->running", schemas.StatusCompleted, schemas.StatusRunning, false},
		{"completed->failed", schemas.StatusCompleted, schemas.StatusFailed, false},
		{"completed->created", schemas.StatusCompleted, schemas.StatusCreated, false},

		{"failed->running", schemas.StatusFailed, schemas.StatusRunning, false},
		{"failed->completed", schemas.StatusFailed, schemas.StatusCompleted, false},
		{"failed->created", schemas.StatusFailed, schemas.StatusCreated, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cp, ctx := newTestCP(t)
			if err := cp.CreateSession(ctx, "ses_1", "t1"); err != nil {
				t.Fatal(err)
			}

			// If we're not at CREATED, transition to the start state
			if tt.from != schemas.StatusCreated {
				if err := cp.Transition(ctx, "ses_1", tt.from); err != nil {
					// It might not be reachable in one hop; chain through valid path
					path := pathTo(cp, ctx, "ses_1", tt.from)
					if path == nil {
						t.Fatalf("cannot reach start state %s: %v", tt.from, err)
					}
				}
			}

			// Verify we're in the expected start state
			s, _ := cp.GetSession("ses_1")
			if s.Status != tt.from {
				t.Fatalf("setup failed: expected %s, got %s", tt.from, s.Status)
			}

			err := cp.Transition(ctx, "ses_1", tt.to)
			s, _ = cp.GetSession("ses_1")

			if tt.valid && err != nil {
				t.Errorf("expected valid transition %s->%s, got error: %v", tt.from, tt.to, err)
			}
			if !tt.valid && err == nil {
				t.Errorf("expected invalid transition %s->%s to error, but it succeeded (status=%s)", tt.from, tt.to, s.Status)
			}
			if tt.valid && s.Status != tt.to {
				t.Errorf("expected status %s after transition, got %s", tt.to, s.Status)
			}
		})
	}
}

// pathTo finds the shortest valid transition path to reach target status.
// Returns nil if the target is unreachable.
func pathTo(cp *ControlPlane, ctx context.Context, sessionID string, target schemas.SessionStatus) []schemas.SessionStatus {
	// BFS over the transition graph
	type node struct {
		status schemas.SessionStatus
		path   []schemas.SessionStatus
	}

	queue := []node{{status: schemas.StatusCreated, path: []schemas.SessionStatus{}}}
	visited := map[schemas.SessionStatus]bool{schemas.StatusCreated: true}

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		next, ok := validTransitions[curr.status]
		if !ok {
			continue
		}

		for _, n := range next {
			if visited[n] {
				continue
			}
			visited[n] = true
			newPath := append(append([]schemas.SessionStatus{}, curr.path...), n)
			if n == target {
				// Apply the path
				for _, s := range newPath {
					if err := cp.Transition(ctx, sessionID, s); err != nil {
						return nil
					}
				}
				return newPath
			}
			queue = append(queue, node{status: n, path: newPath})
		}
	}
	return nil
}

func TestTransitionSessionNotFound(t *testing.T) {
	cp, ctx := newTestCP(t)
	err := cp.Transition(ctx, "nonexistent", schemas.StatusRunning)
	if err == nil {
		t.Fatal("expected error for nonexistent session, got nil")
	}
}

func TestEmit(t *testing.T) {
	// Verify events are published on transitions.
	// We use a MemoryBus and subscribe before each transition.
	bus := eventbus.NewMemoryBus()
	cp := New(bus)
	ctx := context.Background()

	received := make(chan schemas.Event, 10)
	cancel, err := bus.Subscribe(ctx, "foreman.session.>",
		func(_ context.Context, evt schemas.Event) error {
			received <- evt
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	if err := cp.CreateSession(ctx, "ses_1", "t1"); err != nil {
		t.Fatal(err)
	}

	select {
	case evt := <-received:
		if evt.Type != schemas.EvSessionCreated {
			t.Errorf("expected EvSessionCreated, got %s", evt.Type)
		}
	default:
		t.Fatal("expected event on CreateSession")
	}

	// Test: transition emits the right event type
	if err := cp.Transition(ctx, "ses_1", schemas.StatusAllocating); err != nil {
		t.Fatal(err)
	}
	select {
	case evt := <-received:
		if evt.Type != schemas.EvSessionAllocating {
			t.Errorf("expected EvSessionAllocating, got %s", evt.Type)
		}
	default:
		t.Fatal("expected event on Transition")
	}
}

func TestFullHappyPath(t *testing.T) {
	cp, ctx := newTestCP(t)

	if err := cp.CreateSession(ctx, "ses_1", "task_1"); err != nil {
		t.Fatal(err)
	}

	transitions := []schemas.SessionStatus{
		schemas.StatusAllocating,
		schemas.StatusRunning,
		schemas.StatusCompleted,
	}

	for _, s := range transitions {
		if err := cp.Transition(ctx, "ses_1", s); err != nil {
			t.Fatalf("transition to %s: %v", s, err)
		}
	}
}

func TestFullApprovalPath(t *testing.T) {
	cp, ctx := newTestCP(t)

	if err := cp.CreateSession(ctx, "ses_1", "task_1"); err != nil {
		t.Fatal(err)
	}

	transitions := []schemas.SessionStatus{
		schemas.StatusAllocating,
		schemas.StatusRunning,
		schemas.StatusApproval,
		schemas.StatusRunning,
		schemas.StatusCompleted,
	}

	for _, s := range transitions {
		if err := cp.Transition(ctx, "ses_1", s); err != nil {
			t.Fatalf("transition to %s: %v", s, err)
		}
	}
}
