package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"github.com/foreman/foreman/internal/eventbus"
)

// BuildConfig holds the task description and tool bindings for configuring an agent.
type BuildConfig struct {
	TaskDescription string
	SessionID       string
	Tools           []ToolDef
}

// ToolDef describes a tool available to the agent.
type ToolDef struct {
	Name        string
	Description string
	InputSchema any
}

// AgentMeta holds static metadata about the agent framework.
type AgentMeta struct {
	Name    string
	Version string
}

// AgentAdapter is the interface each agent framework must implement.
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

// --- OpenCode JSONL event types ---

// OpenCodeEvent represents one JSON line from "opencode run --format json" output.
type OpenCodeEvent struct {
	Type      string         `json:"type"`
	SessionID string         `json:"sessionID,omitempty"`
	Part      *OpenCodePart  `json:"part,omitempty"`
	Error     *OpenCodeError `json:"error,omitempty"`
}

// OpenCodePart holds the event payload for step/text/tool events.
type OpenCodePart struct {
	Text   string         `json:"text,omitempty"`
	Tool   string         `json:"tool,omitempty"`
	Reason string         `json:"reason,omitempty"`
	Cost   float64        `json:"cost,omitempty"`
	State  *OpenCodeState `json:"state,omitempty"`
}

// OpenCodeState holds tool invocation output.
type OpenCodeState struct {
	Output string `json:"output,omitempty"`
}

// OpenCodeError holds error details from an opencode error event.
type OpenCodeError struct {
	Name string             `json:"name,omitempty"`
	Data *OpenCodeErrorData `json:"data,omitempty"`
}

// OpenCodeErrorData holds the error message.
type OpenCodeErrorData struct {
	Message string `json:"message,omitempty"`
}

// --- OpenCodeAdapter ---

// OpenCodeAdapter drives the opencode CLI in "run" mode (one-shot tasks).
// It configures the agent, parses JSONL output, and provides health checks.
type OpenCodeAdapter struct {
	cmd               string
	cwd               string
	bus               eventbus.EventBus
	heartbeatInterval time.Duration
	heartbeatTimeout  time.Duration
}

// NewOpenCodeAdapter creates a new adapter for the opencode CLI.
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

// BuildConfig returns the CLI args for a one-shot opencode run.
func (a *OpenCodeAdapter) BuildConfig(ctx context.Context, cfg BuildConfig) ([]string, error) {
	return a.buildRunArgs(cfg.TaskDescription), nil
}

// Verify checks that the opencode binary is reachable.
func (a *OpenCodeAdapter) Verify(ctx context.Context) error {
	path, err := exec.LookPath(a.cmd)
	if err != nil {
		return fmt.Errorf("opencode binary %q not found in PATH: %w", a.cmd, err)
	}
	// Quick sanity: run "opencode --version"
	ver, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return fmt.Errorf("opencode version check failed: %w", err)
	}
	if len(ver) == 0 {
		return fmt.Errorf("opencode --version returned empty output")
	}
	return nil
}

// StartCommand returns the command vector. For run mode this is identical
// to BuildConfig; for server mode it would return ["serve", "--port", "..."].
func (a *OpenCodeAdapter) StartCommand(ctx context.Context, config map[string]any) ([]string, error) {
	task, _ := config["task"].(string)
	if task == "" {
		return nil, fmt.Errorf("start config requires a 'task' key")
	}
	return a.buildRunArgs(task), nil
}

// ParseEvent parses one newline-delimited JSON line from the opencode output.
// Returns *OpenCodeEvent on success, or a raw text event for non-JSON lines.
func (a *OpenCodeAdapter) ParseEvent(ctx context.Context, line []byte) (any, error) {
	if len(line) == 0 {
		return nil, nil
	}

	var evt OpenCodeEvent
	if err := json.Unmarshal(line, &evt); err != nil {
		// Not valid JSON -- return as raw text
		return map[string]string{"type": "text", "text": string(line)}, nil
	}
	if evt.Type == "" {
		return map[string]string{"type": "text", "text": string(line)}, nil
	}
	return &evt, nil
}

// InjectPrompt sends additional instructions to the agent.
// In run mode this is a no-op (task is already defined at launch).
// In server mode this would POST to the running agent session.
func (a *OpenCodeAdapter) InjectPrompt(ctx context.Context, prompt string) ([]byte, error) {
	return nil, fmt.Errorf("inject prompt not supported in run mode")
}

func (a *OpenCodeAdapter) HeartbeatTimeout() time.Duration {
	return a.heartbeatTimeout
}

// CheckHealth verifies the opencode binary is still reachable.
func (a *OpenCodeAdapter) CheckHealth(ctx context.Context) error {
	_, err := exec.LookPath(a.cmd)
	return err
}

// buildRunArgs constructs the CLI args for "opencode run --format json ... -p <task>".
func (a *OpenCodeAdapter) buildRunArgs(task string) []string {
	return []string{
		a.cmd, "run", "--format", "json",
		"--permission-mode", "bypassPermissions",
		"-p", task,
	}
}
