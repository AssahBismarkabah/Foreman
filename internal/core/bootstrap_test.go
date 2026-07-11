package core

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/foreman/foreman/internal/config"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp("", "foreman-test-*.yaml")
	if err != nil {
		t.Fatalf("create temp config: %v", err)
	}
	if _, err := f.Write([]byte(content)); err != nil {
		_ = os.Remove(f.Name())
		t.Fatalf("write temp config: %v", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		t.Fatalf("close temp config: %v", err)
	}
	return f.Name()
}

func TestBootstrapMemoryBus(t *testing.T) {
	// Minimal config with in-memory bus and no sandbox.
	yaml := `
subsystems:
  eventbus:
    kind: memory
  coordinator:
    max_concurrent: 5
    default_timeout: 5m
`
	configPath := writeTempConfig(t, yaml)
	defer func() { _ = os.Remove(configPath) }()

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	ctx := context.Background()
	app, err := Bootstrap(ctx, cfg)
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if app == nil {
		t.Fatal("expected non-nil app")
	}
	if app.EventBus == nil {
		t.Error("expected non-nil EventBus")
	}
	if app.ControlPlane == nil {
		t.Error("expected non-nil ControlPlane")
	}
	if app.Coordinator == nil {
		t.Error("expected non-nil Coordinator")
	}
	if app.MCPHub == nil {
		t.Error("expected non-nil MCPHub")
	}

	app.Shutdown(ctx)
}

func TestBootstrapWithDockerAndOpenCode(t *testing.T) {
	// Full config with Docker sandbox and OpenCode adapter.
	yaml := `
subsystems:
  eventbus:
    kind: memory
  sandbox:
    kind: docker
    image: alpine:latest
  coordinator:
    max_concurrent: 5
    default_timeout: 10m
  agents:
    - name: opencode
      kind: opencode
      cmd: opencode
      cwd: /tmp
      heartbeat_interval: 30s
      heartbeat_timeout: 90s
  mcphub:
    servers:
      - name: filesystem
        transport: stdio
        command: npx
        args:
          - "@modelcontextprotocol/server-filesystem"
          - /tmp
`
	configPath := writeTempConfig(t, yaml)
	defer func() { _ = os.Remove(configPath) }()

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	ctx := context.Background()
	app, err := Bootstrap(ctx, cfg)
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if app == nil {
		t.Fatal("expected non-nil app")
	}
	if app.Sandbox == nil {
		t.Error("expected non-nil Sandbox (Docker)")
	}
	if app.MCPHub == nil {
		t.Error("expected non-nil MCPHub")
	}

	app.Shutdown(ctx)
}

func TestBootstrapInvalidSandboxKind(t *testing.T) {
	yaml := `
subsystems:
  eventbus:
    kind: memory
  sandbox:
    kind: nonexistent
  coordinator:
    max_concurrent: 5
`
	configPath := writeTempConfig(t, yaml)
	defer func() { _ = os.Remove(configPath) }()

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	ctx := context.Background()
	_, err = Bootstrap(ctx, cfg)
	if err == nil {
		t.Fatal("expected error for unknown sandbox kind")
	}
}

func TestBootstrapInvalidEventBusKind(t *testing.T) {
	yaml := `
subsystems:
  eventbus:
    kind: nonexistent
  coordinator:
    max_concurrent: 5
`
	configPath := writeTempConfig(t, yaml)
	defer func() { _ = os.Remove(configPath) }()

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	ctx := context.Background()
	_, err = Bootstrap(ctx, cfg)
	if err == nil {
		t.Fatal("expected error for unknown eventbus kind")
	}
}

func TestShutdownClosesBus(t *testing.T) {
	// Verify that Shutdown completes without hanging.
	yaml := `
subsystems:
  eventbus:
    kind: memory
  coordinator:
    max_concurrent: 5
`
	configPath := writeTempConfig(t, yaml)
	defer func() { _ = os.Remove(configPath) }()

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	app, err := Bootstrap(ctx, cfg)
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	// Shutdown should complete quickly
	done := make(chan struct{})
	go func() {
		app.Shutdown(ctx)
		close(done)
	}()

	select {
	case <-done:
		// good
	case <-ctx.Done():
		t.Fatal("Shutdown timed out")
	}
}
