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
