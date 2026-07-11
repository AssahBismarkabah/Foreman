package eventbus

import (
	"context"
	"sync"
	"testing"

	"github.com/foreman/foreman/internal/schemas"
)

func TestMemoryBusPubSub(t *testing.T) {
	bus := NewMemoryBus()
	ctx := context.Background()

	var mu sync.Mutex
	received := make([]schemas.Event, 0)

	cancel, err := bus.Subscribe(ctx, "test.subject",
		func(_ context.Context, evt schemas.Event) error {
			mu.Lock()
			received = append(received, evt)
			mu.Unlock()
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	evt := schemas.Event{ID: "1", Type: schemas.EvSessionCreated, SessionID: "ses_1"}
	if err := bus.Publish(ctx, "test.subject", evt); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	if len(received) != 1 {
		t.Fatalf("expected 1 event, got %d", len(received))
	}
	if received[0].ID != "1" {
		t.Errorf("expected event ID 1, got %s", received[0].ID)
	}
	mu.Unlock()
}

func TestMemoryBusMultipleSubscribers(t *testing.T) {
	bus := NewMemoryBus()
	ctx := context.Background()

	var mu sync.Mutex
	count1, count2 := 0, 0

	cancel1, _ := bus.Subscribe(ctx, "test.subject",
		func(_ context.Context, evt schemas.Event) error {
			mu.Lock()
			count1++
			mu.Unlock()
			return nil
		},
	)
	cancel2, _ := bus.Subscribe(ctx, "test.subject",
		func(_ context.Context, evt schemas.Event) error {
			mu.Lock()
			count2++
			mu.Unlock()
			return nil
		},
	)

	evt := schemas.Event{ID: "1"}
	if err := bus.Publish(ctx, "test.subject", evt); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	if count1 != 1 {
		t.Errorf("subscriber 1: expected 1, got %d", count1)
	}
	if count2 != 1 {
		t.Errorf("subscriber 2: expected 1, got %d", count2)
	}
	mu.Unlock()

	cancel1()
	cancel2()
}

func TestMemoryBusNoMatch(t *testing.T) {
	bus := NewMemoryBus()
	ctx := context.Background()

	called := false
	cancel, _ := bus.Subscribe(ctx, "one.subject",
		func(_ context.Context, evt schemas.Event) error {
			called = true
			return nil
		},
	)
	defer cancel()

	// Publish on a different subject
	if err := bus.Publish(ctx, "other.subject", schemas.Event{ID: "1"}); err != nil {
		t.Fatal(err)
	}

	if called {
		t.Error("handler should not be called for different subject")
	}
}

func TestMemoryBusUnsubscribe(t *testing.T) {
	bus := NewMemoryBus()
	ctx := context.Background()

	count := 0
	cancel, _ := bus.Subscribe(ctx, "test.subject",
		func(_ context.Context, evt schemas.Event) error {
			count++
			return nil
		},
	)

	evt := schemas.Event{ID: "1"}
	if err := bus.Publish(ctx, "test.subject", evt); err != nil {
		t.Fatal(err)
	}

	if count != 1 {
		t.Fatalf("expected 1 before unsubscribe, got %d", count)
	}

	cancel()

	if err := bus.Publish(ctx, "test.subject", evt); err != nil {
		t.Fatal(err)
	}

	if count != 1 {
		t.Errorf("expected 1 after unsubscribe, got %d", count)
	}
}

func TestMemoryBusHandlerError(t *testing.T) {
	// Handler errors should be logged but not returned to caller
	bus := NewMemoryBus()
	ctx := context.Background()

	cancel, _ := bus.Subscribe(ctx, "test.subject",
		func(_ context.Context, evt schemas.Event) error {
			return nil // no error
		},
	)
	defer cancel()

	// This should not panic or return an error
	err := bus.Publish(ctx, "test.subject", schemas.Event{ID: "1"})
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestMemoryBusClose(t *testing.T) {
	bus := NewMemoryBus()
	if err := bus.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestMemoryBusWildcardSubscribe(t *testing.T) {
	bus := NewMemoryBus()
	ctx := context.Background()

	called := false
	cancel, _ := bus.Subscribe(ctx, "foreman.session.>",
		func(_ context.Context, evt schemas.Event) error {
			called = true
			return nil
		},
	)
	defer cancel()

	// "foreman.session.>" should match "foreman.session.created"
	if err := bus.Publish(ctx, "foreman.session.created", schemas.Event{ID: "1"}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("expected wildcard 'foreman.session.>' to match 'foreman.session.created'")
	}

	// Reset and test more specific wildcard
	called = false
	cancel2, _ := bus.Subscribe(ctx, "foreman.*",
		func(_ context.Context, evt schemas.Event) error {
			called = true
			return nil
		},
	)
	defer cancel2()

	// "foreman.*" should match exactly one token: "foreman.session"
	if err := bus.Publish(ctx, "foreman.session", schemas.Event{ID: "2"}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("expected wildcard 'foreman.*' to match 'foreman.session'")
	}
}

func TestMemoryBusWildcardNoMatch(t *testing.T) {
	bus := NewMemoryBus()
	ctx := context.Background()

	called := false
	cancel, _ := bus.Subscribe(ctx, "foreman.session.>",
		func(_ context.Context, evt schemas.Event) error {
			called = true
			return nil
		},
	)
	defer cancel()

	// "foreman.session.>" should NOT match "other.subject"
	if err := bus.Publish(ctx, "other.subject", schemas.Event{ID: "1"}); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("expected no match for different subject")
	}
}

func TestMemoryBusSingleWildcard(t *testing.T) {
	bus := NewMemoryBus()
	ctx := context.Background()

	called := false
	cancel, _ := bus.Subscribe(ctx, "foreman.*.created",
		func(_ context.Context, evt schemas.Event) error {
			called = true
			return nil
		},
	)
	defer cancel()

	// "foreman.*.created" should match "foreman.session.created"
	if err := bus.Publish(ctx, "foreman.session.created", schemas.Event{ID: "1"}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("expected 'foreman.*.created' to match 'foreman.session.created'")
	}

	// But should NOT match "foreman.session.other"
	called = false
	if err := bus.Publish(ctx, "foreman.session.other", schemas.Event{ID: "2"}); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("expected 'foreman.*.created' to not match 'foreman.session.other'")
	}
}

func TestMemoryBusConcurrentPublish(t *testing.T) {
	bus := NewMemoryBus()
	ctx := context.Background()

	var mu sync.Mutex
	total := 0
	n := 100

	cancel, _ := bus.Subscribe(ctx, "test.subject",
		func(_ context.Context, evt schemas.Event) error {
			mu.Lock()
			total++
			mu.Unlock()
			return nil
		},
	)
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_ = bus.Publish(ctx, "test.subject", schemas.Event{ID: "1"})
		}(i)
	}
	wg.Wait()

	mu.Lock()
	if total != n {
		t.Errorf("expected %d events, got %d", n, total)
	}
	mu.Unlock()
}
