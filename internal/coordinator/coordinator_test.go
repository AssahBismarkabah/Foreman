package coordinator

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/foreman/foreman/internal/adapter"
	"github.com/foreman/foreman/internal/controlplane"
	"github.com/foreman/foreman/internal/eventbus"
	"github.com/foreman/foreman/internal/mcphub"
	"github.com/foreman/foreman/internal/sandbox"
	"github.com/foreman/foreman/internal/schemas"
)

// mockAdapter implements the full adapter.AgentAdapter interface.
type mockAdapter struct {
	name string
}

func (m *mockAdapter) Name() string { return m.name }
func (m *mockAdapter) Meta() adapter.AgentMeta {
	return adapter.AgentMeta{Name: m.name, Version: "test"}
}
func (m *mockAdapter) BuildConfig(ctx context.Context, cfg adapter.BuildConfig) ([]string, error) {
	return []string{"echo", "agent-output"}, nil
}
func (m *mockAdapter) Verify(ctx context.Context) error { return nil }
func (m *mockAdapter) StartCommand(ctx context.Context, cfg map[string]any) ([]string, error) {
	return []string{"echo", "agent-output"}, nil
}
func (m *mockAdapter) ParseEvent(ctx context.Context, line []byte) (any, error) {
	return map[string]string{"type": "text", "text": string(line)}, nil
}
func (m *mockAdapter) InjectPrompt(ctx context.Context, prompt string) ([]byte, error) {
	return nil, nil
}
func (m *mockAdapter) HeartbeatTimeout() time.Duration       { return 90 * time.Second }
func (m *mockAdapter) CheckHealth(ctx context.Context) error { return nil }

// mockSandbox implements sandbox.Sandbox with no-op returns.
type mockSandbox struct{}

func (m *mockSandbox) Provision(ctx context.Context, spec sandbox.SandboxSpec) (string, error) {
	return "sandbox_1", nil
}
func (m *mockSandbox) Execute(ctx context.Context, sessionID string, cmd []string, timeout time.Duration) (*sandbox.ExecutionResult, error) {
	return &sandbox.ExecutionResult{ExitCode: 0, Stdout: "mock output", Stderr: ""}, nil
}
func (m *mockSandbox) WriteFile(ctx context.Context, sessionID, path string, content []byte) error {
	return nil
}
func (m *mockSandbox) ReadFile(ctx context.Context, sessionID, path string) ([]byte, error) {
	return nil, nil
}
func (m *mockSandbox) UploadCheckpoint(ctx context.Context, sessionID, sourceDir string) (string, error) {
	return "cp_1", nil
}
func (m *mockSandbox) SubscribeEvents(ctx context.Context, sessionID string) (<-chan sandbox.SandboxEvent, error) {
	return make(chan sandbox.SandboxEvent), nil
}
func (m *mockSandbox) Heartbeat(ctx context.Context, sessionID string) error {
	return nil
}
func (m *mockSandbox) Destroy(ctx context.Context, sessionID string) error {
	return nil
}

func newTestCoordinator(t *testing.T) (*Coordinator, context.Context) {
	t.Helper()
	bus := eventbus.NewMemoryBus()
	cp := controlplane.New(bus)
	hub := mcphub.NewStaticHub(nil)
	sbox := &mockSandbox{}
	adapters := []adapter.AgentAdapter{&mockAdapter{name: "test"}}
	co := New(bus, cp, sbox, hub, adapters, 5)
	return co, context.Background()
}

// waitForStatus polls the control plane until the session reaches the expected
// status or the timeout expires. This is necessary because the coordinator runs
// agents in a goroutine.
func waitForStatus(t *testing.T, cp *controlplane.ControlPlane, sessionID string, status schemas.SessionStatus, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s, ok := cp.GetSession(sessionID)
		if ok && s.Status == status {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	s, ok := cp.GetSession(sessionID)
	if !ok {
		t.Fatalf("session %s not found after waiting", sessionID)
	}
	t.Fatalf("expected status %s after waiting, got %s", status, s.Status)
}

func TestSubmitTask(t *testing.T) {
	co, ctx := newTestCoordinator(t)
	if err := co.SubmitTask(ctx, "task_1", "do something"); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	// The goroutine should progress through ALLOCATING -> RUNNING -> COMPLETED.
	// Wait for COMPLETED (the final state on success).
	waitForStatus(t, co.cp, "ses_task_1", schemas.StatusCompleted, 2*time.Second)
}

func TestSubmitTaskAdapterFailure(t *testing.T) {
	// If the adapter fails, the session should go to FAILED.
	bus := eventbus.NewMemoryBus()
	cp := controlplane.New(bus)
	hub := mcphub.NewStaticHub(nil)
	sbox := &mockSandbox{}

	// Use an adapter that fails during BuildConfig.
	failBuild := &failBuildAdapter{name: "fail-build"}
	adapters := []adapter.AgentAdapter{failBuild}
	co := New(bus, cp, sbox, hub, adapters, 5)

	ctx := context.Background()
	if err := co.SubmitTask(ctx, "task_fail", "will fail"); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	waitForStatus(t, cp, "ses_task_fail", schemas.StatusFailed, 2*time.Second)
}

// failBuildAdapter fails when BuildConfig is called.
type failBuildAdapter struct {
	name string
}

func (f *failBuildAdapter) Name() string            { return f.name }
func (f *failBuildAdapter) Meta() adapter.AgentMeta { return adapter.AgentMeta{Name: f.name} }
func (f *failBuildAdapter) BuildConfig(ctx context.Context, cfg adapter.BuildConfig) ([]string, error) {
	return nil, fmt.Errorf("simulated build failure")
}
func (f *failBuildAdapter) Verify(ctx context.Context) error { return nil }
func (f *failBuildAdapter) StartCommand(ctx context.Context, cfg map[string]any) ([]string, error) {
	return nil, fmt.Errorf("simulated build failure")
}
func (f *failBuildAdapter) ParseEvent(ctx context.Context, line []byte) (any, error) { return nil, nil }
func (f *failBuildAdapter) InjectPrompt(ctx context.Context, prompt string) ([]byte, error) {
	return nil, nil
}
func (f *failBuildAdapter) HeartbeatTimeout() time.Duration       { return time.Minute }
func (f *failBuildAdapter) CheckHealth(ctx context.Context) error { return nil }

// We need fmt for the error format in failBuildAdapter.
// Already imported below.

func TestSubmitTaskMaxConcurrent(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	cp := controlplane.New(bus)
	hub := mcphub.NewStaticHub(nil)
	sbox := &mockSandbox{}
	co := New(bus, cp, sbox, hub, nil, 1)

	ctx := context.Background()

	// First task should succeed
	if err := co.SubmitTask(ctx, "task_1", "first"); err != nil {
		t.Fatal(err)
	}

	// Second task should fail synchronously due to max concurrent
	err := co.SubmitTask(ctx, "task_2", "second")
	if err == nil {
		t.Error("expected error for exceeding max concurrent tasks")
	}
}

func TestCoordinatorAdapterScoping(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	cp := controlplane.New(bus)
	hub := mcphub.NewStaticHub(nil)
	sbox := &mockSandbox{}
	adapters := []adapter.AgentAdapter{
		&mockAdapter{name: "opencode"},
		&mockAdapter{name: "claude"},
	}
	co := New(bus, cp, sbox, hub, adapters, 5)

	if len(co.adapters) != 2 {
		t.Errorf("expected 2 adapters, got %d", len(co.adapters))
	}
	if _, ok := co.adapters["opencode"]; !ok {
		t.Error("expected opencode adapter in map")
	}
	if _, ok := co.adapters["claude"]; !ok {
		t.Error("expected claude adapter in map")
	}
}
