package schemas

import "time"

type EventType string

const (
	// Lifecycle
	EvSessionCreated      EventType = "session.created"
	EvSessionAllocating   EventType = "session.allocating"
	EvSessionRunning      EventType = "session.running"
	EvSessionWaitingInput EventType = "session.waiting_input"
	EvSessionApproval     EventType = "session.approval"
	EvSessionCancelling   EventType = "session.cancelling"
	EvSessionCompleted    EventType = "session.completed"
	EvSessionFailed       EventType = "session.failed"

	// Task
	EvTaskSubmitted EventType = "task.submitted"
	EvTaskAssigned  EventType = "task.assigned"
	EvTaskProgress  EventType = "task.progress"
	EvTaskCompleted EventType = "task.completed"
	EvTaskFailed    EventType = "task.failed"

	// Agent
	EvAgentHeartbeat EventType = "agent.heartbeat"
	EvAgentOutput    EventType = "agent.output"
	EvAgentError     EventType = "agent.error"
	EvAgentDone      EventType = "agent.done"

	// Tool
	EvToolResolved EventType = "tool.resolved"
	EvToolCall     EventType = "tool.call"
	EvToolResult   EventType = "tool.result"

	// User / Communication
	EvUserMessage     EventType = "user.message"
	EvApprovalRequest EventType = "approval.request"
	EvApprovalGranted EventType = "approval.granted"
	EvApprovalDenied  EventType = "approval.denied"
)

type Event struct {
	ID        string    `json:"id"`
	Type      EventType `json:"type"`
	SessionID string    `json:"session_id"`
	Timestamp time.Time `json:"timestamp"`
	Payload   any       `json:"payload"`
}

type SessionPayload struct {
	SessionID string `json:"session_id"`
	TaskID    string `json:"task_id"`
	Status    string `json:"status"`
}

type TaskPayload struct {
	TaskID      string `json:"task_id"`
	Description string `json:"description"`
	Status      string `json:"status"`
}

type AgentHeartbeatPayload struct {
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
	Seq       uint64 `json:"seq"`
}

type ToolCallPayload struct {
	CallID    string `json:"call_id"`
	ToolName  string `json:"tool_name"`
	Args      any    `json:"args"`
	SessionID string `json:"session_id"`
}

// UserMessage is a message from a chat user relayed by a communication plugin.
type UserMessage struct {
	Plugin   string `json:"plugin"` // "slack", "discord"
	UserID   string `json:"user_id"`
	Channel  string `json:"channel"`
	Text     string `json:"text"`
	ThreadTS string `json:"thread_ts,omitempty"`
}

// Message is a simple text message sent to a chat channel.
type Message struct {
	Text   string `json:"text"`
	Thread string `json:"thread,omitempty"`
}

// Block is a rich UI element for Slack Block Kit or Discord embeds.
// Type is the platform-independent kind ("section", "actions", "context").
// Elements contains platform-specific payloads (serialized per-plugin).
type Block struct {
	Type     string           `json:"type"`
	Fields   map[string]any   `json:"fields,omitempty"`
	Elements []map[string]any `json:"elements,omitempty"`
}

// SessionStatus tracks the lifecycle state of a session.
type SessionStatus string

const (
	StatusCreated    SessionStatus = "CREATED"
	StatusAllocating SessionStatus = "ALLOCATING"
	StatusRunning    SessionStatus = "RUNNING"
	StatusApproval   SessionStatus = "APPROVAL"
	StatusCancelling SessionStatus = "CANCELLING"
	StatusCompleted  SessionStatus = "COMPLETED"
	StatusFailed     SessionStatus = "FAILED"
)

// SandboxStatus tracks the lifecycle state of a sandbox.
type SandboxStatus string

const (
	SandboxProvisioning SandboxStatus = "provisioning"
	SandboxRunning      SandboxStatus = "running"
	SandboxStopped      SandboxStatus = "stopped"
	SandboxFailed       SandboxStatus = "failed"
)
