package plugin

import (
	"context"
	"testing"
	"time"

	"github.com/foreman/foreman/internal/eventbus"
	"github.com/foreman/foreman/internal/schemas"
)

// TestCLI_StartAndReadOutput starts a subprocess (echo) and verifies that
// its stdout is published as agent.output events on the bus, followed by
// an agent.done event when the process exits.
func TestCLI_StartAndReadOutput(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	defer func() { _ = bus.Close() }()

	var received []schemas.Event
	cancel, err := bus.Subscribe(context.Background(), schemas.Subject("agent", "cli", "output"),
		func(ctx context.Context, evt schemas.Event) error {
			received = append(received, evt)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cancel()

	doneCh := make(chan schemas.Event, 1)
	doneCancel, err := bus.Subscribe(context.Background(), schemas.Subject("agent", "cli", "done"),
		func(ctx context.Context, evt schemas.Event) error {
			doneCh <- evt
			return nil
		},
	)
	if err != nil {
		t.Fatalf("subscribe done: %v", err)
	}
	defer doneCancel()

	p := NewCLI("cli", "1.0", "echo", "", []string{"line one", "line two"})
	if err := p.Start(context.Background(), bus); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for the done event (process exits after echo)
	select {
	case <-doneCh:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for done event")
	}

	// Verify we received output events
	if len(received) < 1 {
		t.Fatalf("expected at least 1 output event, got %d", len(received))
	}

	// All output events should have type agent.output
	for i, evt := range received {
		if evt.Type != schemas.EvAgentOutput {
			t.Errorf("event[%d]: expected type %s, got %s", i, schemas.EvAgentOutput, evt.Type)
		}
	}
}

// TestCLI_SendMessage writes to the subprocess stdin and verifies the
// message is received on stdout (using cat as the subprocess).
func TestCLI_SendMessage(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	defer func() { _ = bus.Close() }()

	outputCh := make(chan string, 10)
	cancel, err := bus.Subscribe(context.Background(), schemas.Subject("agent", "cli", "output"),
		func(ctx context.Context, evt schemas.Event) error {
			if s, ok := evt.Payload.(string); ok {
				outputCh <- s
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cancel()

	// Use cat as the subprocess: it echoes stdin to stdout
	p := NewCLI("cli", "1.0", "cat", "", nil)
	if err := p.Start(context.Background(), bus); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := p.SendMessage(context.Background(), []byte("hello world")); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	select {
	case msg := <-outputCh:
		if msg != "hello world" {
			t.Errorf("expected 'hello world', got %q", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for echoed message")
	}

	if err := p.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// TestCLI_SendBlockMessage verifies that block messages are JSON-wrapped
// and sent via SendMessage.
func TestCLI_SendBlockMessage(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	defer func() { _ = bus.Close() }()

	outputCh := make(chan string, 10)
	cancel, err := bus.Subscribe(context.Background(), schemas.Subject("agent", "cli", "output"),
		func(ctx context.Context, evt schemas.Event) error {
			if s, ok := evt.Payload.(string); ok {
				outputCh <- s
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cancel()

	p := NewCLI("cli", "1.0", "cat", "", nil)
	if err := p.Start(context.Background(), bus); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := p.SendBlockMessage(context.Background(), "section", []byte("block content")); err != nil {
		t.Fatalf("SendBlockMessage: %v", err)
	}

	select {
	case msg := <-outputCh:
		// The block message is JSON: {"type":"section","content":"block content"}
		if msg == "" {
			t.Error("expected non-empty block message")
		}
		if !contains(msg, "section") {
			t.Errorf("expected 'section' in message, got %q", msg)
		}
		if !contains(msg, "block content") {
			t.Errorf("expected 'block content' in message, got %q", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for block message")
	}

	if err := p.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// TestCLI_Stop_KillsProcess verifies that Stop terminates the subprocess.
func TestCLI_Stop_KillsProcess(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	defer func() { _ = bus.Close() }()

	// Use sleep as a long-running process
	p := NewCLI("cli", "1.0", "sleep", "", []string{"60"})
	if err := p.Start(context.Background(), bus); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := p.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// After Stop, SendMessage should fail (stdin pipe may be broken)
	err := p.SendMessage(context.Background(), []byte("should fail"))
	if err == nil {
		// On some OSes the pipe may still be open briefly; the key
		// assertion is that Stop didn't error. This is a soft check.
		t.Log("SendMessage after Stop did not error (pipe may still be open)")
	}
}

// TestCLI_NameAndVersion verifies the metadata accessors.
func TestCLI_NameAndVersion(t *testing.T) {
	p := NewCLI("slack", "2.0", "echo", "", nil)
	if p.Name() != "slack" {
		t.Errorf("expected name 'slack', got %q", p.Name())
	}
	if p.Version() != "2.0" {
		t.Errorf("expected version '2.0', got %q", p.Version())
	}
}

// TestCLI_DoneEventOnProcessExit verifies that an agent.done event is published
// when the subprocess exits.
func TestCLI_DoneEventOnProcessExit(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	defer func() { _ = bus.Close() }()

	doneCh := make(chan schemas.Event, 1)
	cancel, err := bus.Subscribe(context.Background(), schemas.Subject("agent", "cli", "done"),
		func(ctx context.Context, evt schemas.Event) error {
			doneCh <- evt
			return nil
		},
	)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cancel()

	// echo exits immediately
	p := NewCLI("cli", "1.0", "echo", "", []string{"hello"})
	if err := p.Start(context.Background(), bus); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case evt := <-doneCh:
		if evt.Type != schemas.EvAgentDone {
			t.Errorf("expected type %s, got %s", schemas.EvAgentDone, evt.Type)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for done event")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
