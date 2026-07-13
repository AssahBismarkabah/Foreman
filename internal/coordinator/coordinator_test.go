package coordinator

import (
	"context"
	"fmt"
	"sync"
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
	cp := controlplane.New(bus, nil)
	hub := mcphub.NewStaticHub(nil)
	sbox := &mockSandbox{}
	adapters := []adapter.AgentAdapter{&mockAdapter{name: "test"}}
	co := New(bus, cp, sbox, hub, adapters, nil, 5, nil, 0, 0, 0, 0, 0, 0, 0, 0)
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
	cp := controlplane.New(bus, nil)
	hub := mcphub.NewStaticHub(nil)
	sbox := &mockSandbox{}

	// Use an adapter that fails during BuildConfig.
	failBuild := &failBuildAdapter{name: "fail-build"}
	adapters := []adapter.AgentAdapter{failBuild}
	co := New(bus, cp, sbox, hub, adapters, nil, 5, nil, 0, 0, 0, 0, 0, 0, 0, 0)

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

// blockingAdapter holds the task in BuildConfig until the channel is closed,
// ensuring the session stays active for concurrency checks.
type blockingAdapter struct {
	block chan struct{}
}

func (b *blockingAdapter) Name() string { return "blocking" }
func (b *blockingAdapter) Meta() adapter.AgentMeta {
	return adapter.AgentMeta{Name: "blocking", Version: "test"}
}
func (b *blockingAdapter) BuildConfig(ctx context.Context, cfg adapter.BuildConfig) ([]string, error) {
	<-b.block
	return []string{"echo", "agent-output"}, nil
}
func (b *blockingAdapter) Verify(ctx context.Context) error { return nil }
func (b *blockingAdapter) StartCommand(ctx context.Context, cfg map[string]any) ([]string, error) {
	return nil, nil
}
func (b *blockingAdapter) ParseEvent(ctx context.Context, line []byte) (any, error) {
	return map[string]string{"type": "text", "text": string(line)}, nil
}
func (b *blockingAdapter) InjectPrompt(ctx context.Context, prompt string) ([]byte, error) {
	return nil, nil
}
func (b *blockingAdapter) HeartbeatTimeout() time.Duration       { return time.Minute }
func (b *blockingAdapter) CheckHealth(ctx context.Context) error { return nil }

func TestSubmitTaskMaxConcurrent(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	cp := controlplane.New(bus, nil)
	hub := mcphub.NewStaticHub(nil)
	sbox := &mockSandbox{}

	block := make(chan struct{})
	blkAdapter := &blockingAdapter{block: block}
	co := New(bus, cp, sbox, hub, []adapter.AgentAdapter{blkAdapter}, nil, 1, nil, 0, 0, 0, 0, 0, 0, 0, 0)

	ctx := context.Background()

	// First task -- goroutine will block on BuildConfig, holding the slot
	if err := co.SubmitTask(ctx, "task_1", "first"); err != nil {
		t.Fatal(err)
	}

	// Give the goroutine time to reach the blocking point
	time.Sleep(10 * time.Millisecond)

	// Second task should fail synchronously due to max concurrent
	err := co.SubmitTask(ctx, "task_2", "second")
	if err == nil {
		t.Error("expected error for exceeding max concurrent tasks")
	}

	// Unblock the first task so the goroutine can exit cleanly
	close(block)
}

func TestCoordinatorAdapterScoping(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	cp := controlplane.New(bus, nil)
	hub := mcphub.NewStaticHub(nil)
	sbox := &mockSandbox{}
	adapters := []adapter.AgentAdapter{
		&mockAdapter{name: "opencode"},
		&mockAdapter{name: "claude"},
	}
	co := New(bus, cp, sbox, hub, adapters, nil, 5, nil, 0, 0, 0, 0, 0, 0, 0, 0)

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

// --- Pipeline integration tests ---

// TestFullPipeline_EventsPublishedOnBus verifies that agent.output events
// are published on the event bus when the coordinator runs an agent.
func TestFullPipeline_EventsPublishedOnBus(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	defer func() { _ = bus.Close() }()

	cp := controlplane.New(bus, nil)
	hub := mcphub.NewStaticHub(nil)
	sbox := &mockSandbox{}
	adapters := []adapter.AgentAdapter{&mockAdapter{name: "test"}}
	co := New(bus, cp, sbox, hub, adapters, nil, 5, nil, 0, 0, 0, 0, 0, 0, 0, 0)

	var agentEvents []schemas.Event
	cancel, err := bus.Subscribe(context.Background(),
		schemas.Subject("agent", "ses_task_events", "output"),
		func(ctx context.Context, evt schemas.Event) error {
			agentEvents = append(agentEvents, evt)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cancel()

	if err := co.SubmitTask(context.Background(), "task_events", "do something"); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	waitForStatus(t, cp, "ses_task_events", schemas.StatusCompleted, 2*time.Second)

	if len(agentEvents) == 0 {
		t.Fatal("expected at least 1 agent.output event, got 0")
	}
	for i, evt := range agentEvents {
		if evt.Type != schemas.EvAgentOutput {
			t.Errorf("event[%d]: expected type %s, got %s", i, schemas.EvAgentOutput, evt.Type)
		}
		if evt.SessionID != "ses_task_events" {
			t.Errorf("event[%d]: expected sessionID 'ses_task_events', got %q", i, evt.SessionID)
		}
	}
}

// TestFullPipeline_NonZeroExitCode verifies that a non-zero agent exit code
// transitions the session to FAILED.
func TestFullPipeline_NonZeroExitCode(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	defer func() { _ = bus.Close() }()

	cp := controlplane.New(bus, nil)
	hub := mcphub.NewStaticHub(nil)
	sbox := &failSandbox{exitCode: 42, stderr: "agent crashed"}
	adapters := []adapter.AgentAdapter{&mockAdapter{name: "test"}}
	co := New(bus, cp, sbox, hub, adapters, nil, 5, nil, 0, 0, 0, 0, 0, 0, 0, 0)

	if err := co.SubmitTask(context.Background(), "task_fail_exit", "do something"); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	waitForStatus(t, cp, "ses_task_fail_exit", schemas.StatusFailed, 2*time.Second)
}

// TestFullPipeline_SandboxProvisionFailure verifies that a sandbox provision
// error transitions the session to FAILED.
func TestFullPipeline_SandboxProvisionFailure(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	defer func() { _ = bus.Close() }()

	cp := controlplane.New(bus, nil)
	hub := mcphub.NewStaticHub(nil)
	sbox := &provisionFailSandbox{}
	adapters := []adapter.AgentAdapter{&mockAdapter{name: "test"}}
	co := New(bus, cp, sbox, hub, adapters, nil, 5, nil, 0, 0, 0, 0, 0, 0, 0, 0)

	if err := co.SubmitTask(context.Background(), "task_provision_fail", "do something"); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	waitForStatus(t, cp, "ses_task_provision_fail", schemas.StatusFailed, 2*time.Second)
}

// TestFullPipeline_VerifyFailure verifies that an adapter Verify failure
// transitions the session to FAILED.
func TestFullPipeline_VerifyFailure(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	defer func() { _ = bus.Close() }()

	cp := controlplane.New(bus, nil)
	hub := mcphub.NewStaticHub(nil)
	sbox := &mockSandbox{}
	adapters := []adapter.AgentAdapter{&verifyFailAdapter{name: "test"}}
	co := New(bus, cp, sbox, hub, adapters, nil, 5, nil, 0, 0, 0, 0, 0, 0, 0, 0)

	if err := co.SubmitTask(context.Background(), "task_verify_fail", "do something"); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	waitForStatus(t, cp, "ses_task_verify_fail", schemas.StatusFailed, 2*time.Second)
}

// TestFullPipeline_MultiLineOutput verifies that multi-line stdout is parsed
// line by line and each line produces an agent.output event.
func TestFullPipeline_MultiLineOutput(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	defer func() { _ = bus.Close() }()

	cp := controlplane.New(bus, nil)
	hub := mcphub.NewStaticHub(nil)
	sbox := &multiLineSandbox{stdout: "line one\nline two\nline three\n"}
	adapters := []adapter.AgentAdapter{&mockAdapter{name: "test"}}
	co := New(bus, cp, sbox, hub, adapters, nil, 5, nil, 0, 0, 0, 0, 0, 0, 0, 0)

	var agentEvents []schemas.Event
	cancel, err := bus.Subscribe(context.Background(),
		schemas.Subject("agent", "ses_task_multi", "output"),
		func(ctx context.Context, evt schemas.Event) error {
			agentEvents = append(agentEvents, evt)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cancel()

	if err := co.SubmitTask(context.Background(), "task_multi", "do something"); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	waitForStatus(t, cp, "ses_task_multi", schemas.StatusCompleted, 2*time.Second)

	if len(agentEvents) != 3 {
		t.Fatalf("expected 3 agent.output events (one per line), got %d", len(agentEvents))
	}
}

// --- Additional mock types ---

// failSandbox returns a non-zero exit code from Execute.
type failSandbox struct {
	exitCode int
	stderr   string
}

func (m *failSandbox) Provision(ctx context.Context, spec sandbox.SandboxSpec) (string, error) {
	return "sandbox_fail", nil
}
func (m *failSandbox) Execute(ctx context.Context, sessionID string, cmd []string, timeout time.Duration) (*sandbox.ExecutionResult, error) {
	return &sandbox.ExecutionResult{ExitCode: m.exitCode, Stdout: "", Stderr: m.stderr}, nil
}
func (m *failSandbox) WriteFile(ctx context.Context, sessionID, path string, content []byte) error {
	return nil
}
func (m *failSandbox) ReadFile(ctx context.Context, sessionID, path string) ([]byte, error) {
	return nil, nil
}
func (m *failSandbox) UploadCheckpoint(ctx context.Context, sessionID, sourceDir string) (string, error) {
	return "cp_1", nil
}
func (m *failSandbox) SubscribeEvents(ctx context.Context, sessionID string) (<-chan sandbox.SandboxEvent, error) {
	return make(chan sandbox.SandboxEvent), nil
}
func (m *failSandbox) Heartbeat(ctx context.Context, sessionID string) error { return nil }
func (m *failSandbox) Destroy(ctx context.Context, sessionID string) error   { return nil }

// provisionFailSandbox returns an error from Provision.
type provisionFailSandbox struct{}

func (m *provisionFailSandbox) Provision(ctx context.Context, spec sandbox.SandboxSpec) (string, error) {
	return "", fmt.Errorf("docker daemon not available")
}
func (m *provisionFailSandbox) Execute(ctx context.Context, sessionID string, cmd []string, timeout time.Duration) (*sandbox.ExecutionResult, error) {
	return &sandbox.ExecutionResult{ExitCode: 0}, nil
}
func (m *provisionFailSandbox) WriteFile(ctx context.Context, sessionID, path string, content []byte) error {
	return nil
}
func (m *provisionFailSandbox) ReadFile(ctx context.Context, sessionID, path string) ([]byte, error) {
	return nil, nil
}
func (m *provisionFailSandbox) UploadCheckpoint(ctx context.Context, sessionID, sourceDir string) (string, error) {
	return "cp_1", nil
}
func (m *provisionFailSandbox) SubscribeEvents(ctx context.Context, sessionID string) (<-chan sandbox.SandboxEvent, error) {
	return make(chan sandbox.SandboxEvent), nil
}
func (m *provisionFailSandbox) Heartbeat(ctx context.Context, sessionID string) error { return nil }
func (m *provisionFailSandbox) Destroy(ctx context.Context, sessionID string) error   { return nil }

// verifyFailAdapter fails when Verify is called.
type verifyFailAdapter struct {
	name string
}

func (v *verifyFailAdapter) Name() string            { return v.name }
func (v *verifyFailAdapter) Meta() adapter.AgentMeta { return adapter.AgentMeta{Name: v.name} }
func (v *verifyFailAdapter) BuildConfig(ctx context.Context, cfg adapter.BuildConfig) ([]string, error) {
	return []string{"echo", "hello"}, nil
}
func (v *verifyFailAdapter) Verify(ctx context.Context) error {
	return fmt.Errorf("binary not found")
}
func (v *verifyFailAdapter) StartCommand(ctx context.Context, cfg map[string]any) ([]string, error) {
	return []string{"echo", "hello"}, nil
}
func (v *verifyFailAdapter) ParseEvent(ctx context.Context, line []byte) (any, error) {
	return map[string]string{"type": "text", "text": string(line)}, nil
}
func (v *verifyFailAdapter) InjectPrompt(ctx context.Context, prompt string) ([]byte, error) {
	return nil, nil
}
func (v *verifyFailAdapter) HeartbeatTimeout() time.Duration       { return time.Minute }
func (v *verifyFailAdapter) CheckHealth(ctx context.Context) error { return nil }

// multiLineSandbox returns multi-line stdout from Execute.
type multiLineSandbox struct {
	stdout string
}

func (m *multiLineSandbox) Provision(ctx context.Context, spec sandbox.SandboxSpec) (string, error) {
	return "sandbox_multi", nil
}
func (m *multiLineSandbox) Execute(ctx context.Context, sessionID string, cmd []string, timeout time.Duration) (*sandbox.ExecutionResult, error) {
	return &sandbox.ExecutionResult{ExitCode: 0, Stdout: m.stdout, Stderr: ""}, nil
}
func (m *multiLineSandbox) WriteFile(ctx context.Context, sessionID, path string, content []byte) error {
	return nil
}
func (m *multiLineSandbox) ReadFile(ctx context.Context, sessionID, path string) ([]byte, error) {
	return nil, nil
}
func (m *multiLineSandbox) UploadCheckpoint(ctx context.Context, sessionID, sourceDir string) (string, error) {
	return "cp_1", nil
}
func (m *multiLineSandbox) SubscribeEvents(ctx context.Context, sessionID string) (<-chan sandbox.SandboxEvent, error) {
	return make(chan sandbox.SandboxEvent), nil
}
func (m *multiLineSandbox) Heartbeat(ctx context.Context, sessionID string) error { return nil }
func (m *multiLineSandbox) Destroy(ctx context.Context, sessionID string) error   { return nil }

// heartbeatFailSandbox returns success from Heartbeat for the first call,
// then returns errors. Execute blocks until the context is cancelled.
// This simulates a sandbox that starts healthy then crashes.
type heartbeatFailSandbox struct {
	mu        sync.Mutex
	callCount int
}

func (m *heartbeatFailSandbox) Provision(ctx context.Context, spec sandbox.SandboxSpec) (string, error) {
	return "sandbox_hb_fail", nil
}
func (m *heartbeatFailSandbox) Execute(ctx context.Context, sessionID string, cmd []string, timeout time.Duration) (*sandbox.ExecutionResult, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (m *heartbeatFailSandbox) WriteFile(ctx context.Context, sessionID, path string, content []byte) error {
	return nil
}
func (m *heartbeatFailSandbox) ReadFile(ctx context.Context, sessionID, path string) ([]byte, error) {
	return nil, nil
}
func (m *heartbeatFailSandbox) UploadCheckpoint(ctx context.Context, sessionID, sourceDir string) (string, error) {
	return "cp_1", nil
}
func (m *heartbeatFailSandbox) SubscribeEvents(ctx context.Context, sessionID string) (<-chan sandbox.SandboxEvent, error) {
	return make(chan sandbox.SandboxEvent), nil
}
func (m *heartbeatFailSandbox) Heartbeat(ctx context.Context, sessionID string) error {
	m.mu.Lock()
	m.callCount++
	count := m.callCount
	m.mu.Unlock()
	if count > 1 {
		return fmt.Errorf("container not running")
	}
	return nil
}
func (m *heartbeatFailSandbox) Destroy(ctx context.Context, sessionID string) error { return nil }

// blockingSandbox blocks Execute until context is cancelled.
type blockingSandbox struct{}

func (m *blockingSandbox) Provision(ctx context.Context, spec sandbox.SandboxSpec) (string, error) {
	return "sandbox_block", nil
}
func (m *blockingSandbox) Execute(ctx context.Context, sessionID string, cmd []string, timeout time.Duration) (*sandbox.ExecutionResult, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (m *blockingSandbox) WriteFile(ctx context.Context, sessionID, path string, content []byte) error {
	return nil
}
func (m *blockingSandbox) ReadFile(ctx context.Context, sessionID, path string) ([]byte, error) {
	return nil, nil
}
func (m *blockingSandbox) UploadCheckpoint(ctx context.Context, sessionID, sourceDir string) (string, error) {
	return "cp_1", nil
}
func (m *blockingSandbox) SubscribeEvents(ctx context.Context, sessionID string) (<-chan sandbox.SandboxEvent, error) {
	return make(chan sandbox.SandboxEvent), nil
}
func (m *blockingSandbox) Heartbeat(ctx context.Context, sessionID string) error { return nil }
func (m *blockingSandbox) Destroy(ctx context.Context, sessionID string) error   { return nil }

func TestCoordinatorCrashDetectionAndRetry(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	cp := controlplane.New(bus, nil)
	hub := mcphub.NewStaticHub(nil)
	sbox := &heartbeatFailSandbox{}
	adapters := []adapter.AgentAdapter{&mockAdapter{name: "test"}}

	// Enable heartbeat monitoring with fast detection:
	// 50ms interval, 200ms timeout = 4 consecutive failures before crash declared
	// maxRetries=1 for a fast test.
	co := New(bus, cp, sbox, hub, adapters, nil, 5, nil,
		50*time.Millisecond,  // heartbeatInterval
		200*time.Millisecond, // heartbeatTimeout
		1,                    // maxRetries
		10*time.Millisecond,  // backoffBase (small for fast test)
		1.5,                  // backoffMultiplier
		5*time.Millisecond,   // jitter
		0, 0,                 // no resource alerts
	)

	ctx := context.Background()
	if err := co.SubmitTask(ctx, "task_crash", "will crash"); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	// The session should go through ALLOCATING -> RUNNING -> CRASH -> FAILED
	// The crash should be detected within ~200ms, then 1 retry happens
	// (which also fails), so total time ~600ms. Use 10s timeout for safety.
	waitForStatus(t, cp, "ses_task_crash", schemas.StatusFailed, 10*time.Second)

	s, ok := cp.GetSession("ses_task_crash")
	if !ok {
		t.Fatal("session not found")
	}
	if s.Status != schemas.StatusFailed {
		t.Fatalf("expected FAILED, got %s", s.Status)
	}
}

func TestCoordinatorGracefulShutdown(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	cp := controlplane.New(bus, nil)
	hub := mcphub.NewStaticHub(nil)
	sbox := &blockingSandbox{}
	adapters := []adapter.AgentAdapter{&mockAdapter{name: "test"}}
	co := New(bus, cp, sbox, hub, adapters, nil, 5, nil, 0, 0, 0, 0, 0, 0, 0, 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Submit a blocking task
	if err := co.SubmitTask(ctx, "task_shutdown", "blocking task"); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	// Wait for it to reach RUNNING
	waitForStatus(t, cp, "ses_task_shutdown", schemas.StatusRunning, 5*time.Second)

	// Stop accepting new tasks
	co.StopAccepting()

	// New tasks should be rejected
	if err := co.SubmitTask(ctx, "task_rejected", "should fail"); err == nil {
		t.Error("expected error after StopAccepting")
	}

	// Cancel the parent context to unblock the active session's Execute
	cancel()

	// Drain should complete quickly after the session unblocks
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer drainCancel()
	remaining := co.Drain(drainCtx)
	t.Logf("Drain returned %d remaining sessions", remaining)

	// The session should have failed (Execute returned context.Canceled)
	waitForStatus(t, cp, "ses_task_shutdown", schemas.StatusFailed, 2*time.Second)
}
