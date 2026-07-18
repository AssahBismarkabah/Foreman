# Agent Adapter Reference

This document defines the Agent Adapter interface and shared types used by Foreman to abstract different agent frameworks.

## Adapter Interface

```go
type AgentAdapter interface {
    // Metadata
    Name() string
    Version() string
    Capabilities() []Capability

    // Lifecycle
    // BuildConfig produces the configuration for launching this agent
    BuildConfig(spec TaskSpec, tools []ToolBinding) (AgentConfig, error)
    // Verify checks that the agent runtime is present in the sandbox
    Verify(ctx context.Context, sandbox Sandbox) error
    // StartCommand returns the command vector to launch the agent
    StartCommand(config AgentConfig) []string
    // StartEnv returns additional environment variables for the agent
    StartEnv(config AgentConfig) map[string]string

    // Communication
    // ParseEvent translates one line/unit of agent output into a structured event
    ParseEvent(line string) (*AgentEvent, error)
    // InjectPrompt injects instructions/prompts into the agent's context
    InjectPrompt(ctx context.Context, sandbox Sandbox, prompt string) error

    // Health
    // HeartbeatTimeout returns the expected max interval between heartbeats
    HeartbeatTimeout() time.Duration
    // CheckHealth performs a liveness check on the agent
    CheckHealth(ctx context.Context, sandbox Sandbox) error
}
```

## Shared Types

### TaskSpec

Describes a task submitted by a user via a communication plugin. Created by the Control Plane and passed to the adapter.

```go
type TaskSpec struct {
    SessionID string   `json:"session_id"`
    UserID    string   `json:"user_id"`
    Prompt    string   `json:"prompt"`
    RepoURL   string   `json:"repo_url,omitempty"`
    Branch    string   `json:"branch,omitempty"`
    Files     []string `json:"files,omitempty"`  // specific files to work on
    Context   string   `json:"context,omitempty"` // additional context (diffs, logs, errors)
}
```

### AgentConfig

Produced by `BuildConfig` and consumed by the Coordinator. Tells the Coordinator how to launch the agent inside a sandbox.

```go
type AgentConfig struct {
    Command   []string          `json:"command"`             // argv[0] + args
    Env       map[string]string `json:"env,omitempty"`       // additional environment variables
    WorkDir   string            `json:"work_dir,omitempty"`  // working directory inside sandbox
    MCPConfig *MCPAgentConfig   `json:"mcp_config,omitempty"` // MCP servers for this agent
}

type MCPAgentConfig struct {
    Servers []MCPServerBinding `json:"servers"`
}

type MCPServerBinding struct {
    Name      string `json:"name"`
    Transport string `json:"transport"` // "stdio" or "sse"
    Command   string `json:"command,omitempty"` // for stdio servers
    Args      []string `json:"args,omitempty"`
    URL       string `json:"url,omitempty"`     // for SSE servers
}
```

### AgentEvent

Structured output from parsing one unit of agent output. The adapter's `ParseEvent` method translates raw agent stdout/stderr into these normalized events for the Control Plane.

```go
type AgentEvent struct {
    Type      AgentEventType `json:"type"`
    Content   string         `json:"content,omitempty"`
    ToolName  string         `json:"tool_name,omitempty"`
    ToolInput map[string]any `json:"tool_input,omitempty"`
    ToolResult string        `json:"tool_result,omitempty"`
    Error     string         `json:"error,omitempty"`
    Metadata  map[string]any `json:"metadata,omitempty"`
}

type AgentEventType string

const (
    AgentEventText      AgentEventType = "text"       // model-generated text
    AgentEventToolUse   AgentEventType = "tool_use"    // agent called a tool
    AgentEventToolResult AgentEventType = "tool_result" // tool returned a result
    AgentEventError     AgentEventType = "error"       // agent reported an error
    AgentEventDone      AgentEventType = "done"        // agent finished its task
)
```

## Capability Model

Each agent declares what it can do:

```go
type Capability string

const (
    CapFileRead      Capability = "file.read"
    CapFileWrite     Capability = "file.write"
    CapGitRead       Capability = "git.read"
    CapGitWrite      Capability = "git.write"
    CapTestRun       Capability = "test.run"
    CapWebSearch     Capability = "web.search"
    CapWebFetch      Capability = "web.fetch"
    CapTerminalExec  Capability = "terminal.exec"
    CapPRCreate      Capability = "pr.create"
    CapReview        Capability = "code.review"
    CapDeploy        Capability = "deploy"
)
```

The Control Plane uses this to match tasks to agents: if a task requires `pr.create` and the agent doesn't support it, the system either selects a different agent or rejects the task early.

## Adapter Examples

### Claude Code Adapter

Claude Code runs as a subprocess. The adapter:

1. Verifies `claude` binary is in the sandbox PATH
2. Sets environment variables (`ANTHROPIC_API_KEY`, `CLAUDE_CODE_CONFIG`)
3. Launches Claude in headless mode:

```bash
claude --bare -p "<task>" \
  --output-format json \
  --allowedTools "Read,Edit,Bash(git *),Bash(npm *),Bash(go *),Grep" \
  --max-turns 40 \
  --max-budget-usd 2 \
  --permission-mode bypassPermissions
```

4. Parses JSON output: reads `.result` for answer text, `.session_id` for continuation, `.total_cost_usd` for cost tracking
5. Exit code 0 = success; non-zero = failure (check stderr for details). Always parse JSON output rather than relying on exit codes alone.
6. For multi-turn sessions: capture session ID from JSON output, then call with `--resume <session_id>`
7. Claude's built-in MCP support is used directly, with tools scoped via `--allowedTools`

**Important:** `--permission-mode bypassPermissions` requires a one-time interactive acceptance on first run. Set `skipDangerousModePermissionPrompt: true` in `~/.claude/settings.json` for fully unattended operation. In sandboxed environments this is safe -- the flag name is deliberately alarming.

**Alternative SDK path:** Anthropic also publishes Agent SDKs (`anthropic-agent-sdk` for Python, `@anthropic-ai/claude-agent-sdk` for TypeScript) that wrap the same CLI as a subprocess internally, providing higher-level programmatic APIs.

### OpenCode Adapter

OpenCode can be driven in two ways:

**Path A -- Server Mode (Preferred):**

Start `opencode serve --port 4096` as a background process (HTTP API):
- `POST /session` -- create a new session
- `POST /session/:id/message` -- submit a message/task
- `GET /event` -- SSE stream for session events
- `POST /session/:id/abort` -- cancel a running session

This is the most reliable approach for persistent, multi-turn agent sessions.

**Path B -- Run Mode (One-shot tasks):**

```bash
opencode run --format json --model anthropic/claude-sonnet-4-5 \
  --agent build "Implement the login feature"
```

Output is newline-delimited JSON (JSONL) on stdout with five event types:

| Event | When | Key Fields |
|-------|------|-----------|
| `step_start` | Processing begins | `sessionID` |
| `tool_use` | Tool completes | `part.tool`, `part.state.output` |
| `text` | Model text output | `part.text` |
| `step_finish` | Step ends | `part.reason` (stop/tool-calls), `part.cost` |
| `error` | Session error | `error.name`, `error.data.message` |

**Critical Caveats:**
- Permission system has hard-coded defaults. Built-in agents include deny/ask rules (e.g., `external_directory: ask`) that require TUI interaction even with `--auto`.
- Exit codes are unreliable (often 0 even on failure). Always parse the JSONL stream for `error` events or wait for `step_finish` with `reason: stop`.
- Session permission presets are set at bootstrap with `question: deny`, `plan_enter: deny`, `plan_exit: deny` and cannot be overridden via config.
- The server mode (Path A) provides more control by working around permission limitations through the HTTP API.
