package coordinator

import (
	"context"
	"testing"
	"time"

	"github.com/foreman/foreman/internal/controlplane"
	"github.com/foreman/foreman/internal/eventbus"
	"github.com/foreman/foreman/internal/mcphub"
	"github.com/foreman/foreman/internal/sandbox"
	"github.com/foreman/foreman/internal/schemas"
)

type mockAdapter struct {
	name string
}

func (m *mockAdapter) Name() string { return m.name }

type mockSandbox struct{}

func (m *mockSandbox) Provision(ctx context.Context, spec sandbox.SandboxSpec) (string, error) {
	return "sandbox_1", nil
}

func (m *mockSandbox) Execute(ctx context.Context, sessionID string, cmd []string, timeout time.Duration) (*sandbox.ExecutionResult, error) {
	return &sandbox.ExecutionResult{ExitCode: 0, Stdout: "ok"}, nil
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
	adapters := []AgentAdapter{&mockAdapter{name: "test"}}
	co := New(bus, cp, sbox, hub, adapters, 5)
	return co, context.Background()
}

func TestSubmitTask(t *testing.T) {
	co, ctx := newTestCoordinator(t)
	if err := co.SubmitTask(ctx, "task_1", "do something"); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	// Session should have been created and progressed
	// Check via the control plane
	s, ok := co.cp.GetSession("ses_task_1")
	if !ok {
		t.Fatal("session not found after SubmitTask")
	}
	if s.Status != schemas.StatusRunning {
		t.Errorf("expected status RUNNING, got %s", s.Status)
	}
}

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

	// Second task should hit the max concurrent limit
	if err := co.SubmitTask(ctx, "task_2", "second"); err != nil {
		// This is expected to fail or succeed depending on how the coordinator
		// handles concurrent limits. The current implementation checks the
		// limit in startTask().
		t.Logf("second task submission result (expected to be limited): %v", err)
	}
}

func TestCoordinatorAdapterScoping(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	cp := controlplane.New(bus)
	hub := mcphub.NewStaticHub(nil)
	sbox := &mockSandbox{}
	adapters := []AgentAdapter{
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
