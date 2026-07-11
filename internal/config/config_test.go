package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadValidYAML(t *testing.T) {
	yaml := `
subsystems:
  eventbus:
    kind: nats
    url: nats://localhost:4222
  sandbox:
    kind: docker
    image: ubuntu:22.04
  coordinator:
    max_concurrent: 10
    default_timeout: 10m
  agents:
    - name: opencode
      kind: opencode
      cmd: npx opencode
      cwd: /tmp
      heartbeat_interval: 15s
      heartbeat_timeout: 45s
  mcphub:
    servers:
      - name: filesystem
        transport: stdio
        command: npx
        args:
          - "@modelcontextprotocol/server-filesystem"
          - /tmp
`
	f, err := os.CreateTemp("", "foreman-test-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	if _, err := f.Write([]byte(yaml)); err != nil {
		t.Fatal(err)
	}
	f.Close()

	cfg, err := Load(f.Name())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Subsystems.EventBus.Kind != "nats" {
		t.Errorf("eventbus kind = %q, want nats", cfg.Subsystems.EventBus.Kind)
	}
	if cfg.Subsystems.EventBus.URL != "nats://localhost:4222" {
		t.Errorf("eventbus url = %q", cfg.Subsystems.EventBus.URL)
	}
	if cfg.Subsystems.Sandbox.Kind != "docker" {
		t.Errorf("sandbox kind = %q, want docker", cfg.Subsystems.Sandbox.Kind)
	}
	if cfg.Subsystems.Sandbox.Image != "ubuntu:22.04" {
		t.Errorf("sandbox image = %q", cfg.Subsystems.Sandbox.Image)
	}
	if cfg.Subsystems.Coordinator.MaxConcurrent != 10 {
		t.Errorf("max_concurrent = %d, want 10", cfg.Subsystems.Coordinator.MaxConcurrent)
	}
	if cfg.Subsystems.Coordinator.DefaultTimeout != 10*time.Minute {
		t.Errorf("default_timeout = %v, want 10m", cfg.Subsystems.Coordinator.DefaultTimeout)
	}
	if len(cfg.Subsystems.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(cfg.Subsystems.Agents))
	}
	if cfg.Subsystems.Agents[0].Name != "opencode" {
		t.Errorf("agent name = %q", cfg.Subsystems.Agents[0].Name)
	}
	if len(cfg.Subsystems.MCPHub.Servers) != 1 {
		t.Fatalf("expected 1 MCP server, got %d", len(cfg.Subsystems.MCPHub.Servers))
	}
	if cfg.Subsystems.MCPHub.Servers[0].Name != "filesystem" {
		t.Errorf("mcp server name = %q", cfg.Subsystems.MCPHub.Servers[0].Name)
	}
}

func TestLoadMinimalYAML(t *testing.T) {
	yaml := `subsystems: {}`
	f, err := os.CreateTemp("", "foreman-test-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	if _, err := f.Write([]byte(yaml)); err != nil {
		t.Fatal(err)
	}
	f.Close()

	cfg, err := Load(f.Name())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// All fields should be at zero values
	if cfg.Subsystems.EventBus.Kind != "" {
		t.Errorf("expected empty eventbus kind, got %q", cfg.Subsystems.EventBus.Kind)
	}
	if cfg.Subsystems.Coordinator.MaxConcurrent != 0 {
		t.Errorf("expected 0 max_concurrent, got %d", cfg.Subsystems.Coordinator.MaxConcurrent)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path/foreman.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	f, err := os.CreateTemp("", "foreman-test-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	if _, err := f.Write([]byte("{invalid: yaml: unclosed")); err != nil {
		t.Fatal(err)
	}
	f.Close()

	_, err = Load(f.Name())
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}
