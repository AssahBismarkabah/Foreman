# Event Bus Reference

This document defines the Event Bus interface, subject hierarchy, event schemas, and implementation details used by Foreman.

## Overview

The Event Bus is the nervous system of Foreman. All async communication flows through it.

Technology: NATS (lightweight, embedded or clustered). Core NATS is used for at-most-once events such as heartbeats and logs. JetStream is used for at-least-once delivery when needed, such as session events and checkpoints.

Serialization: JSON over NATS. Standard Go `encoding/json` serialization of typed structs. JSON is chosen over Protobuf for simplicity, debuggability, and because Foreman is Go-only (no cross-language schema management needed). NATS subjects use dot-notation hierarchy for routing.

## Interface

The core contract for all async pub/sub. Components do not depend on NATS directly -- they depend on this interface:

```go
type EventBus interface {
    // Publish sends a message to a subject. For critical events, the
    // implementation uses JetStream for at-least-once delivery.
    Publish(ctx context.Context, subject string, data []byte) error

    // Subscribe registers a handler for messages matching a subject pattern.
    // Patterns use NATS-style wildcards: * matches one token, > matches
    // the remainder. Returns a close function to unsubscribe.
    Subscribe(ctx context.Context, subject string, handler EventHandler) (func() error, error)
}

type EventHandler func(ctx context.Context, msg EventMessage)

type EventMessage struct {
    Subject string
    Data    []byte
    Timestamp time.Time
    // Ack acknowledges the message. Required for JetStream consumers.
    Ack func() error
    // Nak marks the message as failed for redelivery. Optional.
    Nak func() error
}
```

The `Ack`/`Nak` functions on `EventMessage` allow the handler to signal success or failure without depending on the underlying transport (Core NATS vs JetStream).

## Subject Hierarchy

Events are routed on NATS-style dot-notation subjects:

- `foreman.session.<action>` -- session lifecycle events
- `foreman.agent.<action>` -- agent runtime events
- `foreman.approval.<action>` -- approval flow events
- `foreman.command.<action>` -- internal commands (Control Plane -> Coordinator)
- `foreman.plugin.<name>.<event>` -- plugin I/O

## Event Metadata

Every event embeds standard metadata:

```go
type EventMeta struct {
    EventID   string    `json:"event_id"`
    SessionID string    `json:"session_id,omitempty"`
    Source    string    `json:"source"`
    Timestamp time.Time `json:"timestamp"`
}
```

## Session Events

Subject prefix: `foreman.session.<action>`

| Event | Subject | Schema | Delivery |
|-------|---------|--------|----------|
| Created | `foreman.session.created` | `SessionCreated` | JetStream |
| State change | `foreman.session.state` | `SessionStateChanged` | JetStream |
| Blocked | `foreman.session.blocked` | `SessionBlocked` | JetStream |
| Completed | `foreman.session.completed` | `SessionCompleted` | JetStream |
| Failed | `foreman.session.failed` | `SessionFailed` | JetStream |
| Cancelled | `foreman.session.cancelled` | `SessionCancelled` | JetStream |

```go
type SessionCreated struct {
    Meta    EventMeta `json:"meta"`
    UserID  string    `json:"user_id"`
    Plugin  string    `json:"plugin"`
    Task    string    `json:"task"`
    Channel string    `json:"channel,omitempty"`
}

type SessionStateChanged struct {
    Meta     EventMeta `json:"meta"`
    OldState string    `json:"old_state"`
    NewState string    `json:"new_state"`
    Reason   string    `json:"reason,omitempty"`
}

type SessionBlocked struct {
    Meta    EventMeta `json:"meta"`
    Reason  string    `json:"reason"`
    Policy  string    `json:"policy,omitempty"`
    Summary string    `json:"summary"`
    Diff    string    `json:"diff,omitempty"`
}

type SessionCompleted struct {
    Meta     EventMeta `json:"meta"`
    Result   string    `json:"result"`
    Summary  string    `json:"summary"`
    Duration string    `json:"duration"`
}

type SessionFailed struct {
    Meta    EventMeta `json:"meta"`
    Error   string    `json:"error"`
    Attempt int       `json:"attempt"`
}

type SessionCancelled struct {
    Meta   EventMeta `json:"meta"`
    Reason string    `json:"reason"`
}
```

## Agent Events

Subject prefix: `foreman.agent.<action>`

| Event | Subject | Schema | Delivery |
|-------|---------|--------|----------|
| Heartbeat | `foreman.agent.heartbeat` | `AgentHeartbeat` | Core NATS |
| Log | `foreman.agent.log` | `AgentLog` | Core NATS |
| Checkpoint | `foreman.agent.checkpoint` | `AgentCheckpoint` | JetStream |
| Crash | `foreman.agent.crash` | `AgentCrash` | JetStream |

```go
type AgentHeartbeat struct {
    Meta         EventMeta `json:"meta"`
    AgentID      string    `json:"agent_id"`
    SandboxID    string    `json:"sandbox_id"`
    CPUUsage     float64   `json:"cpu_usage,omitempty"`
    MemoryUsage  float64   `json:"memory_usage,omitempty"`
}

type AgentLog struct {
    Meta    EventMeta `json:"meta"`
    AgentID string    `json:"agent_id"`
    Stream  string    `json:"stream"` // stdout, stderr
    Line    string    `json:"line"`
}

type AgentCheckpoint struct {
    Meta     EventMeta       `json:"meta"`
    AgentID  string          `json:"agent_id"`
    Snapshot json.RawMessage `json:"snapshot"`
    Step     string          `json:"step,omitempty"`
}

type AgentCrash struct {
    Meta      EventMeta `json:"meta"`
    AgentID   string    `json:"agent_id"`
    Signal    string    `json:"signal,omitempty"`
    ExitCode  int       `json:"exit_code"`
    LogTail   string    `json:"log_tail,omitempty"`
}
```

## Approval Events

Subject prefix: `foreman.approval.<action>`

| Event | Subject | Schema | Delivery |
|-------|---------|--------|----------|
| Required | `foreman.approval.required` | `ApprovalRequired` | JetStream |
| Granted | `foreman.approval.granted` | `ApprovalResponse` | JetStream |
| Denied | `foreman.approval.denied` | `ApprovalResponse` | JetStream |

```go
type ApprovalRequired struct {
    Meta      EventMeta `json:"meta"`
    Policy    string    `json:"policy"`
    Action    string    `json:"action"`
    Target    string    `json:"target"`
    Summary   string    `json:"summary"`
    Diff      string    `json:"diff,omitempty"`
    Approvers []string  `json:"approvers"`
    Timeout   int       `json:"timeout"` // seconds
}

type ApprovalResponse struct {
    Meta      EventMeta `json:"meta"`
    Approved  bool      `json:"approved"`
    Responder string    `json:"responder"`
    Reason    string    `json:"reason,omitempty"`
}
```

## Internal Commands

Subject prefix: `foreman.command.<action>`

| Event | Subject | Schema | Delivery |
|-------|---------|--------|----------|
| Allocate | `foreman.command.allocate` | `AllocateCommand` | JetStream |
| Teardown | `foreman.command.teardown` | `TeardownCommand` | JetStream |

```go
type AllocateCommand struct {
    Meta        EventMeta     `json:"meta"`
    AgentType   string        `json:"agent_type"`
    TaskSpec    json.RawMessage `json:"task_spec"`
    SandboxType SandboxType   `json:"sandbox_type"`
    Tools       []string      `json:"tools"`
}

type TeardownCommand struct {
    Meta      EventMeta `json:"meta"`
    AgentID   string    `json:"agent_id"`
    Reason    string    `json:"reason"`
    Force     bool      `json:"force"`
}
```

## Implementation

### Stream Configuration

Single `foreman-events` stream with `FileStorage` and `LimitsPolicy` retention -- messages kept for 14 days regardless of consumption, enabling replay for crash recovery:

```go
streamConfig := jetstream.StreamConfig{
    Name:     "foreman-events",
    Subjects: []string{
        "foreman.session.>",
        "foreman.agent.>",
        "foreman.approval.>",
        "foreman.command.>",
        "foreman.plugin.>",
    },
    Storage:      jetstream.FileStorage,
    Retention:    jetstream.LimitsPolicy,
    MaxAge:       14 * 24 * time.Hour,
    DuplicatesWindow: 2 * time.Minute,
}
```

### Consumer Pattern

All event processors use pull consumers with explicit acknowledgements. Pull consumers give the application control over fetch rate (built-in backpressure). Durable consumers auto-resume from the last acknowledged message on restart -- no manual recovery logic needed.

```go
consumer, _ := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
    Durable:       "foreman-session-worker",
    AckPolicy:     jetstream.AckExplicitPolicy,
    DeliverPolicy: jetstream.DeliverAllPolicy,
    MaxAckPending: 100,
    MaxDeliver:    10,
    AckWait:       30 * time.Second,
})
```

### Exactly-Once Delivery

1. **Publish deduplication:** Each event carries a unique ID via `Nats-Msg-Id` header; NATS server drops duplicates within a 2-minute window.
2. **Consumer ack policy:** `AckExplicitPolicy` requires every message to be individually acknowledged. Un-acked messages are redelivered (up to `MaxDeliver`).
3. **Idempotent handlers:** Event processors are designed to handle duplicate deliveries safely (check-then-act pattern).

### Crash Recovery

- On restart, durable consumers automatically resume from the last acknowledged sequence position.
- For full state rebuild, create a temporary consumer with `DeliverByStartTimePolicy` to replay events from a specific point in time.
- Core NATS subjects (heartbeats, logs) are at-most-once and lost on crash -- acceptable for transient data.

### Deployment

- **Development/testing:** Embedded NATS server running in-process with the Foreman binary. Single binary deployment, zero external dependencies.
- **Production:** Separate NATS server cluster (3 nodes minimum), JetStream enabled, file-based storage with replication factor 3.
- **Monitoring:** Prometheus NATS exporter + Grafana dashboard; key metric is `jetstream_consumer_ack_pending` for slow consumer detection.

```go
// Embedded NATS for development
import "github.com/nats-io/nats-server/v2/server"

opts := &server.Options{
    Port:           -1,           // random port
    JetStream:      true,
    JetStreamMaxStore: 10 * 1024 * 1024 * 1024,
}
ns, err := server.NewServer(opts)
go ns.Start()
nc, err := nats.Connect(ns.ClientURL())
```
