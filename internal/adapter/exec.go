package adapter

import (
	"context"
	"fmt"
	"time"
)

// ExecAdapter runs a shell command directly inside the sandbox.
// It is useful for testing the execution pipeline end-to-end or for
// simple "run a script" workflows that don't need a full agent framework.
type ExecAdapter struct {
	cmd              string
	cwd              string
	heartbeatTimeout time.Duration
}

// NewExecAdapter creates an adapter that runs cmd with args derived from the
// task description. The cmd should be a shell (e.g. "sh") since the adapter
// invokes `cmd -c <description>`.
func NewExecAdapter(cmd, cwd string, hbTimeout time.Duration) *ExecAdapter {
	if cmd == "" {
		cmd = "sh"
	}
	return &ExecAdapter{
		cmd:              cmd,
		cwd:              cwd,
		heartbeatTimeout: hbTimeout,
	}
}

func (a *ExecAdapter) Name() string { return "exec" }

func (a *ExecAdapter) Meta() AgentMeta {
	return AgentMeta{Name: "exec", Version: "0.1"}
}

// BuildConfig returns ["sh", "-c", description].
func (a *ExecAdapter) BuildConfig(_ context.Context, cfg BuildConfig) ([]string, error) {
	if cfg.TaskDescription == "" {
		return nil, fmt.Errorf("exec adapter: task description is required")
	}
	return []string{a.cmd, "-c", cfg.TaskDescription}, nil
}

// Verify always returns nil -- no host-side binary is needed.
func (a *ExecAdapter) Verify(_ context.Context) error {
	return nil
}

// StartCommand returns the command vector for the task.
func (a *ExecAdapter) StartCommand(_ context.Context, config map[string]any) ([]string, error) {
	task, _ := config["task"].(string)
	if task == "" {
		return nil, fmt.Errorf("exec adapter: start config requires a 'task' key")
	}
	return []string{a.cmd, "-c", task}, nil
}

// ParseEvent returns the raw line as a text event.
func (a *ExecAdapter) ParseEvent(_ context.Context, line []byte) (any, error) {
	if len(line) == 0 {
		return nil, nil
	}
	return map[string]string{"type": "text", "text": string(line)}, nil
}

// InjectPrompt is not supported in run mode.
func (a *ExecAdapter) InjectPrompt(_ context.Context, _ string) ([]byte, error) {
	return nil, fmt.Errorf("exec adapter: inject prompt not supported in run mode")
}

func (a *ExecAdapter) HeartbeatTimeout() time.Duration {
	return a.heartbeatTimeout
}

// CheckHealth always returns nil.
func (a *ExecAdapter) CheckHealth(_ context.Context) error {
	return nil
}
