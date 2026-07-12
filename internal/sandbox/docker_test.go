package sandbox

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func dockerAvailable() bool {
	return exec.Command("docker", "info", "--format", "{{.ServerVersion}}").Run() == nil
}

func skipNoDocker(t *testing.T) {
	t.Helper()
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}
}

func newTestDockerSandbox(t *testing.T) *DockerSandbox {
	t.Helper()
	s, err := NewDockerSandbox("alpine:latest")
	if err != nil {
		t.Fatalf("NewDockerSandbox: %v", err)
	}
	return s
}

func TestDockerSandbox_ProvisionAndDestroy(t *testing.T) {
	skipNoDocker(t)

	ctx := context.Background()
	s := newTestDockerSandbox(t)

	sessionID, err := s.Provision(ctx, SandboxSpec{Image: "alpine:latest"})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	defer func() {
		if derr := s.Destroy(ctx, sessionID); derr != nil {
			t.Errorf("Destroy: %v", derr)
		}
	}()

	if sessionID == "" {
		t.Fatal("expected non-empty sessionID")
	}

	// Heartbeat should pass (container is running)
	if err := s.Heartbeat(ctx, sessionID); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
}

func TestDockerSandbox_Execute(t *testing.T) {
	skipNoDocker(t)

	ctx := context.Background()
	s := newTestDockerSandbox(t)

	sessionID, err := s.Provision(ctx, SandboxSpec{Image: "alpine:latest"})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	defer func() {
		if derr := s.Destroy(ctx, sessionID); derr != nil {
			t.Logf("Destroy: %v", derr)
		}
	}()

	res, err := s.Execute(ctx, sessionID, []string{"echo", "hello world"}, 5*time.Second)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code %d, want 0", res.ExitCode)
	}
	if strings.TrimSpace(res.Stdout) != "hello world" {
		t.Fatalf("stdout %q, want %q", res.Stdout, "hello world")
	}
}

func TestDockerSandbox_WriteAndReadFile(t *testing.T) {
	skipNoDocker(t)

	ctx := context.Background()
	s := newTestDockerSandbox(t)

	sessionID, err := s.Provision(ctx, SandboxSpec{Image: "alpine:latest"})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	defer func() {
		if derr := s.Destroy(ctx, sessionID); derr != nil {
			t.Logf("Destroy: %v", derr)
		}
	}()

	content := []byte("hello from foreman test")
	if err := s.WriteFile(ctx, sessionID, "/tmp/test.txt", content); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := s.ReadFile(ctx, sessionID, "/tmp/test.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("read %q, want %q", string(got), string(content))
	}
}

func TestDockerSandbox_ExecuteWithExitCode(t *testing.T) {
	skipNoDocker(t)

	ctx := context.Background()
	s := newTestDockerSandbox(t)

	sessionID, err := s.Provision(ctx, SandboxSpec{Image: "alpine:latest"})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	defer func() {
		if derr := s.Destroy(ctx, sessionID); derr != nil {
			t.Logf("Destroy: %v", derr)
		}
	}()

	res, err := s.Execute(ctx, sessionID, []string{"sh", "-c", "exit 42"}, 5*time.Second)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.ExitCode != 42 {
		t.Fatalf("exit code %d, want 42", res.ExitCode)
	}
}

func TestDockerSandbox_SubscribeEvents(t *testing.T) {
	skipNoDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	s := newTestDockerSandbox(t)

	sessionID, err := s.Provision(ctx, SandboxSpec{Image: "alpine:latest"})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	// Destroy in defer regardless
	defer func() {
		if derr := s.Destroy(ctx, sessionID); derr != nil {
			t.Logf("Destroy: %v", derr)
		}
	}()

	// SubscribeEvents returns a channel that follows container logs.
	// Note: docker exec output does NOT appear in docker logs -- only PID 1 output
	// does (the keep-alive tail -f /dev/null). This method is best-effort for
	// container-level events.
	eventCh, err := s.SubscribeEvents(ctx, sessionID)
	if err != nil {
		t.Fatalf("SubscribeEvents: %v", err)
	}
	if eventCh == nil {
		t.Fatal("expected non-nil event channel")
	}
}

func TestDockerSandbox_DestroyNonexistent(t *testing.T) {
	skipNoDocker(t)

	ctx := context.Background()
	s := newTestDockerSandbox(t)

	// Destroying a non-existent sandbox should return an error
	err := s.Destroy(ctx, "nonexistent-sandbox")
	if err == nil {
		t.Fatal("expected error when destroying non-existent sandbox")
	}
}
