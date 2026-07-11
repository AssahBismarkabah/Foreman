package mcphub

import (
	"context"
	"testing"
)

func TestNewStaticHub_Empty(t *testing.T) {
	hub := NewStaticHub(nil)
	tools, err := hub.ResolveTools(context.Background(), "ses_1")
	if err != nil {
		t.Fatalf("ResolveTools: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(tools))
	}
	servers, err := hub.ListServers(context.Background())
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if len(servers) != 0 {
		t.Errorf("expected 0 servers, got %d", len(servers))
	}
}

func TestNewStaticHub_WithServers(t *testing.T) {
	cfgs := []MCPServerConfig{
		{Name: "filesystem", Transport: "stdio", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-filesystem"}},
		{Name: "github", Transport: "stdio", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-github"}},
	}
	hub := NewStaticHub(cfgs)

	tools, err := hub.ResolveTools(context.Background(), "ses_1")
	if err != nil {
		t.Fatalf("ResolveTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if tools[0].Name != "filesystem" {
		t.Errorf("expected tool[0] name 'filesystem', got %q", tools[0].Name)
	}
	if tools[0].ServerName != "filesystem" {
		t.Errorf("expected tool[0] server 'filesystem', got %q", tools[0].ServerName)
	}
	if tools[1].Name != "github" {
		t.Errorf("expected tool[1] name 'github', got %q", tools[1].Name)
	}
}

func TestResolveTools_ReturnsConsistentResults(t *testing.T) {
	cfgs := []MCPServerConfig{
		{Name: "filesystem"},
		{Name: "github"},
		{Name: "test-runner"},
	}
	hub := NewStaticHub(cfgs)

	tools1, _ := hub.ResolveTools(context.Background(), "ses_a")
	tools2, _ := hub.ResolveTools(context.Background(), "ses_b")
	if len(tools1) != len(tools2) {
		t.Fatalf("expected same tool count for different sessions, got %d vs %d", len(tools1), len(tools2))
	}
	if len(tools1) != 3 {
		t.Errorf("expected 3 tools, got %d", len(tools1))
	}
}

func TestRegisterServer_AddsToList(t *testing.T) {
	hub := NewStaticHub(nil)

	err := hub.RegisterServer(context.Background(), MCPServerConfig{
		Name:      "new-server",
		Transport: "stdio",
		Command:   "npx",
		Args:      []string{"-y", "some-mcp-server"},
	})
	if err != nil {
		t.Fatalf("RegisterServer: %v", err)
	}

	servers, _ := hub.ListServers(context.Background())
	if len(servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(servers))
	}
	if servers[0].Name != "new-server" {
		t.Errorf("expected name 'new-server', got %q", servers[0].Name)
	}
}

func TestRegisterServer_Multiple(t *testing.T) {
	hub := NewStaticHub([]MCPServerConfig{{Name: "existing"}})

	_ = hub.RegisterServer(context.Background(), MCPServerConfig{Name: "second"})
	_ = hub.RegisterServer(context.Background(), MCPServerConfig{Name: "third"})

	servers, _ := hub.ListServers(context.Background())
	if len(servers) != 3 {
		t.Fatalf("expected 3 servers, got %d", len(servers))
	}
	expected := []string{"existing", "second", "third"}
	for i, name := range expected {
		if servers[i].Name != name {
			t.Errorf("expected server[%d] name %q, got %q", i, name, servers[i].Name)
		}
	}
}

func TestListServers_AfterRegister(t *testing.T) {
	hub := NewStaticHub([]MCPServerConfig{
		{Name: "initial", Transport: "stdio", Command: "npx"},
	})

	servers, _ := hub.ListServers(context.Background())
	if len(servers) != 1 {
		t.Fatalf("expected 1 server initially, got %d", len(servers))
	}

	_ = hub.RegisterServer(context.Background(), MCPServerConfig{Name: "added"})

	servers, _ = hub.ListServers(context.Background())
	if len(servers) != 2 {
		t.Fatalf("expected 2 servers after register, got %d", len(servers))
	}
}
