package mcphub

import (
	"context"
)

type ToolBinding struct {
	Name        string
	ServerName  string
	Description string
	InputSchema any
}

type MCPServerConfig struct {
	Name      string
	Transport string
	Command   string
	Args      []string
}

type MCPHub interface {
	ResolveTools(ctx context.Context, sessionID string) ([]ToolBinding, error)
	RegisterServer(ctx context.Context, cfg MCPServerConfig) error
	ListServers(ctx context.Context) ([]MCPServerConfig, error)
}

type StaticHub struct {
	servers []MCPServerConfig
	tools   []ToolBinding
}

func NewStaticHub(cfgs []MCPServerConfig) *StaticHub {
	tools := make([]ToolBinding, 0)
	for _, s := range cfgs {
		tools = append(tools, ToolBinding{
			Name:       s.Name,
			ServerName: s.Name,
		})
	}
	return &StaticHub{
		servers: cfgs,
		tools:   tools,
	}
}

func (h *StaticHub) ResolveTools(ctx context.Context, sessionID string) ([]ToolBinding, error) {
	return h.tools, nil
}

func (h *StaticHub) RegisterServer(ctx context.Context, cfg MCPServerConfig) error {
	h.servers = append(h.servers, cfg)
	return nil
}

func (h *StaticHub) ListServers(ctx context.Context) ([]MCPServerConfig, error) {
	return h.servers, nil
}
