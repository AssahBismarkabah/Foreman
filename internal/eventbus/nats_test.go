package eventbus

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/foreman/foreman/internal/schemas"
)

// newTestNATSBus creates a fresh embedded NATS bus with a clean stream.
func newTestNATSBus(ctx context.Context, t *testing.T) *NATSBus {
	t.Helper()
	bus, err := NewEmbeddedNATSBus(ctx)
	if err != nil {
		t.Fatalf("NewEmbeddedNATSBus: %v", err)
	}
	// Purge any messages from previous tests so consumers with DeliverAllPolicy
	// only see messages published during this test.
	if err := bus.stream.Purge(ctx); err != nil {
		_ = bus.Close()
		t.Fatalf("purge stream: %v", err)
	}
	return bus
}

func TestNATSEmbeddedPublishSubscribe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bus := newTestNATSBus(ctx, t)
	defer func() {
		if cerr := bus.Close(); cerr != nil {
			t.Errorf("bus.Close: %v", cerr)
		}
	}()

	// Subscribe before publishing
	received := make(chan schemas.Event, 1)
	unsub, err := bus.Subscribe(ctx, "foreman.test.>", func(ctx context.Context, evt schemas.Event) error {
		received <- evt
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsub()

	// Give consumer time to be ready
	time.Sleep(500 * time.Millisecond)

	// Publish an event
	evt := schemas.Event{
		ID:        "test-1",
		Type:      schemas.EvSessionCreated,
		SessionID: "sess-abc",
		Timestamp: time.Now(),
		Payload: schemas.SessionPayload{
			SessionID: "sess-abc",
			TaskID:    "task-1",
			Status:    string(schemas.StatusCreated),
		},
	}

	if err := bus.Publish(ctx, "foreman.test.1", evt); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Wait for event or timeout
	select {
	case got := <-received:
		if got.ID != evt.ID {
			t.Errorf("got event ID %q, want %q", got.ID, evt.ID)
		}
		if got.SessionID != evt.SessionID {
			t.Errorf("got SessionID %q, want %q", got.SessionID, evt.SessionID)
		}
		if got.Type != evt.Type {
			t.Errorf("got Type %q, want %q", got.Type, evt.Type)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for event")
	}
}

func TestNATSEmbeddedMultipleSubscribers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bus := newTestNATSBus(ctx, t)
	defer func() {
		_ = bus.Close()
	}()

	var mu sync.Mutex
	count1, count2 := 0, 0

	unsub1, err := bus.Subscribe(ctx, "foreman.test.>", func(ctx context.Context, evt schemas.Event) error {
		mu.Lock()
		count1++
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe 1: %v", err)
	}
	defer unsub1()

	unsub2, err := bus.Subscribe(ctx, "foreman.test.>", func(ctx context.Context, evt schemas.Event) error {
		mu.Lock()
		count2++
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe 2: %v", err)
	}
	defer unsub2()

	time.Sleep(500 * time.Millisecond)

	evt := schemas.Event{ID: "test-multi", Type: schemas.EvSessionCreated, Timestamp: time.Now()}
	if err := bus.Publish(ctx, "foreman.test.multi", evt); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	time.Sleep(2 * time.Second)

	mu.Lock()
	if count1 != 1 {
		t.Errorf("subscriber 1 got %d events, want 1", count1)
	}
	if count2 != 1 {
		t.Errorf("subscriber 2 got %d events, want 1", count2)
	}
	mu.Unlock()
}

func TestNATSEmbeddedWildcardSubject(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bus := newTestNATSBus(ctx, t)
	defer func() {
		_ = bus.Close()
	}()

	received := make(chan schemas.Event, 2)
	unsub, err := bus.Subscribe(ctx, "foreman.session.>", func(ctx context.Context, evt schemas.Event) error {
		received <- evt
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsub()

	time.Sleep(500 * time.Millisecond)

	evt1 := schemas.Event{ID: "w1", Type: schemas.EvSessionCreated, Timestamp: time.Now()}
	evt2 := schemas.Event{ID: "w2", Type: schemas.EvSessionRunning, Timestamp: time.Now()}

	if err := bus.Publish(ctx, "foreman.session.created", evt1); err != nil {
		t.Fatalf("Publish 1: %v", err)
	}
	if err := bus.Publish(ctx, "foreman.session.running", evt2); err != nil {
		t.Fatalf("Publish 2: %v", err)
	}

	for i := 0; i < 2; i++ {
		select {
		case <-received:
		case <-ctx.Done():
			t.Fatal("timeout waiting for event")
		}
	}
}

func TestNATSEmbeddedNoCrossTalk(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bus := newTestNATSBus(ctx, t)
	defer func() {
		_ = bus.Close()
	}()

	received := make(chan schemas.Event, 1)
	unsub, err := bus.Subscribe(ctx, "foreman.session.>", func(ctx context.Context, evt schemas.Event) error {
		received <- evt
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsub()

	time.Sleep(500 * time.Millisecond)

	// Publish to a subject that shouldn't match (session.> vs agent.heartbeat)
	evt := schemas.Event{ID: "no-match", Type: schemas.EvAgentHeartbeat, Timestamp: time.Now()}
	if err := bus.Publish(ctx, "foreman.agent.heartbeat", evt); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Wait a bit -- should not receive anything
	time.Sleep(2 * time.Second)

	select {
	case <-received:
		t.Fatal("received event on non-matching subject")
	default:
		// good -- no cross-talk
	}
}

func TestNATSEmbeddedPubBeforeSub(t *testing.T) {
	// Verify that publishing before subscribing misses the message (DeliverAllPolicy
	// but the consumer is created after publish, and the test starts with a clean
	// stream -- but this publishes before sub, so the sub won't get it).
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bus := newTestNATSBus(ctx, t)
	defer func() {
		_ = bus.Close()
	}()

	// Publish first
	evt := schemas.Event{ID: "pub-before-sub", Type: schemas.EvSessionCreated, Timestamp: time.Now()}
	if err := bus.Publish(ctx, "foreman.test.presub", evt); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Now subscribe
	received := make(chan schemas.Event, 1)
	unsub, err := bus.Subscribe(ctx, "foreman.test.>", func(ctx context.Context, evt schemas.Event) error {
		received <- evt
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsub()

	time.Sleep(2 * time.Second)

	// Should get the message because DeliverAllPolicy replays from the beginning
	select {
	case got := <-received:
		if got.ID != evt.ID {
			t.Errorf("got event ID %q, want %q", got.ID, evt.ID)
		}
	case <-ctx.Done():
		t.Fatal("timeout -- message not received despite DeliverAllPolicy")
	}
}
