package adapter

import (
	"context"
	"time"

	"github.com/foreman/foreman/internal/eventbus"
)

type BuildConfig struct {
	TaskDescription string
	SessionID       string
	Tools           []ToolDef
}

type ToolDef struct {
	Name        string
	Description string
	InputSchema any
}

type AgentMeta struct {
	Name    string
	Version string
}

type AgentAdapter interface {
	Name() string
	Meta() AgentMeta
	BuildConfig(ctx context.Context, cfg BuildConfig) ([]string, error)
	Verify(ctx context.Context) error
	StartCommand(ctx context.Context, config map[string]any) ([]string, error)
	ParseEvent(ctx context.Context, line []byte) (any, error)
	InjectPrompt(ctx context.Context, prompt string) ([]byte, error)
	HeartbeatTimeout() time.Duration
	CheckHealth(ctx context.Context) error
}

type OpenCodeAdapter struct {
	cmd               string
	cwd               string
	bus               eventbus.EventBus
	heartbeatInterval time.Duration
	heartbeatTimeout  time.Duration
}

func NewOpenCodeAdapter(cmd, cwd string, bus eventbus.EventBus, hbInterval, hbTimeout time.Duration) *OpenCodeAdapter {
	return &OpenCodeAdapter{
		cmd:               cmd,
		cwd:               cwd,
		bus:               bus,
		heartbeatInterval: hbInterval,
		heartbeatTimeout:  hbTimeout,
	}
}

func (a *OpenCodeAdapter) Name() string { return "opencode" }

func (a *OpenCodeAdapter) Meta() AgentMeta {
	return AgentMeta{Name: "opencode", Version: "0.1"}
}

func (a *OpenCodeAdapter) BuildConfig(ctx context.Context, cfg BuildConfig) ([]string, error) {
	args := []string{
		a.cmd, "run", "--format", "json",
		"--permission-mode", "bypassPermissions",
		"-p", cfg.TaskDescription,
	}
	return args, nil
}

func (a *OpenCodeAdapter) Verify(ctx context.Context) error {
	return nil
}

func (a *OpenCodeAdapter) StartCommand(ctx context.Context, config map[string]any) ([]string, error) {
	return nil, nil
}

func (a *OpenCodeAdapter) ParseEvent(ctx context.Context, line []byte) (any, error) {
	return nil, nil
}

func (a *OpenCodeAdapter) InjectPrompt(ctx context.Context, prompt string) ([]byte, error) {
	return nil, nil
}

func (a *OpenCodeAdapter) HeartbeatTimeout() time.Duration {
	return a.heartbeatTimeout
}

func (a *OpenCodeAdapter) CheckHealth(ctx context.Context) error {
	return nil
}
