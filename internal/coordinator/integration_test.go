package coordinator

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/foreman/foreman/internal/adapter"
	"github.com/foreman/foreman/internal/controlplane"
	"github.com/foreman/foreman/internal/eventbus"
	"github.com/foreman/foreman/internal/mcphub"
	"github.com/foreman/foreman/internal/sandbox"
	"github.com/foreman/foreman/internal/schemas"
)

// This test proves the Foundation checkpoint: coordinator -> real Docker ->
// mock adapter -> session COMPLETED -> container destroyed.
//
// It is the only test that wires real infrastructure (Docker) through the
// coordinator pipeline. All other coordinator tests use mock sandboxes.

func dockerAvailable() bool {
	return exec.Command("docker", "info", "--format", "{{.ServerVersion}}").Run() == nil
}

func skipNoDocker(t *testing.T) {
	t.Helper()
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}
}

// echoAdapter is a mock adapter that returns "echo" as the agent command.
// This lets us run a real command in a real Docker container without
// depending on opencode being installed.
type echoAdapter struct{}

func (a *echoAdapter) Name() string { return "echo-agent" }
func (a *echoAdapter) Meta() adapter.AgentMeta {
	return adapter.AgentMeta{Name: "echo-agent", Version: "test"}
}
func (a *echoAdapter) BuildConfig(ctx context.Context, cfg adapter.BuildConfig) ([]string, error) {
	return []string{"echo", "hello from docker"}, nil
}
func (a *echoAdapter) Verify(ctx context.Context) error { return nil }
func (a *echoAdapter) StartCommand(ctx context.Context, cfg map[string]any) ([]string, error) {
	return []string{"echo", "hello from docker"}, nil
}
func (a *echoAdapter) ParseEvent(ctx context.Context, line []byte) (any, error) {
	return map[string]string{"type": "text", "text": string(line)}, nil
}
func (a *echoAdapter) InjectPrompt(ctx context.Context, prompt string) ([]byte, error) {
	return nil, nil
}
func (a *echoAdapter) HeartbeatTimeout() time.Duration       { return 90 * time.Second }
func (a *echoAdapter) CheckHealth(ctx context.Context) error { return nil }

func TestIntegration_CoordinatorWithRealDocker(t *testing.T) {
	skipNoDocker(t)

	bus := eventbus.NewMemoryBus()
	defer func() { _ = bus.Close() }()

	cp := controlplane.New(bus, nil)
	hub := mcphub.NewStaticHub(nil)
	sbox, err := sandbox.NewDockerSandbox("alpine:latest")
	if err != nil {
		t.Fatalf("NewDockerSandbox: %v", err)
	}
	adapters := []adapter.AgentAdapter{&echoAdapter{}}
	co := New(bus, cp, sbox, hub, adapters, nil, 5, nil, 0, 0, 0, 0, 0, 0, 0, 0, 0)

	// Subscribe to agent.output events to verify they are published
	var agentEvents []schemas.Event
	cancel, err := bus.Subscribe(context.Background(),
		schemas.Subject("agent", "ses_task_integration", "output"),
		func(ctx context.Context, evt schemas.Event) error {
			agentEvents = append(agentEvents, evt)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cancel()

	// Submit the task
	ctx := context.Background()
	if err := co.SubmitTask(ctx, "task_integration", "echo hello from docker"); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	// Wait for COMPLETED
	waitForStatus(t, cp, "ses_task_integration", schemas.StatusCompleted, 30*time.Second)

	// Verify at least one agent.output event was published
	if len(agentEvents) == 0 {
		t.Fatal("expected at least 1 agent.output event, got 0")
	}

	// Verify the event contains the echo output
	found := false
	for _, evt := range agentEvents {
		if evt.Type != schemas.EvAgentOutput {
			t.Errorf("expected event type %s, got %s", schemas.EvAgentOutput, evt.Type)
		}
		if evt.SessionID != "ses_task_integration" {
			t.Errorf("expected sessionID 'ses_task_integration', got %q", evt.SessionID)
		}
		// The payload is a map[string]string with "text" key
		if m, ok := evt.Payload.(map[string]string); ok {
			if m["text"] == "hello from docker" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("expected agent.output event with text 'hello from docker'")
	}

	// Verify the session is in COMPLETED state
	s, ok := cp.GetSession("ses_task_integration")
	if !ok {
		t.Fatal("session not found")
	}
	if s.Status != schemas.StatusCompleted {
		t.Fatalf("expected session status COMPLETED, got %s", s.Status)
	}

	// Verify the container was cleaned up by the coordinator.
	// The cleanup happens after the COMPLETED transition, so we need to poll.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		dockerPs := exec.Command("docker", "ps", "--filter", "name=foreman-sbox-", "--format", "{{.Names}}")
		out, _ := dockerPs.Output()
		if len(out) == 0 {
			return // container cleaned up successfully
		}
		time.Sleep(200 * time.Millisecond)
	}

	// If we get here, the container is still running after 10s
	dockerPs := exec.Command("docker", "ps", "--filter", "name=foreman-sbox-", "--format", "{{.Names}}")
	out, _ := dockerPs.Output()
	t.Errorf("expected no running foreman containers after cleanup, got: %s", string(out))
}
