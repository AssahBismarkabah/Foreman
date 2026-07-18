# Communication Plugin Reference

This document defines the Communication Plugin interface used by Foreman to integrate with chat platforms such as Slack and Discord.

## Plugin Interface

Each communication platform implements this interface to connect to Foreman:

```go
type Plugin interface {
    // Identity
    Name() string
    Version() string

    // Lifecycle
    Start(ctx context.Context, bus EventBus) error
    Stop(ctx context.Context) error

    // Outbound messages
    SendMessage(ctx context.Context, channel string, msg Message) error
    SendBlockMessage(ctx context.Context, channel string, blocks []Block) error
}
```

## Message Types

`Message` is a simple text message for chat platforms.

```go
type Message struct {
    Text    string `json:"text"`
    Thread  string `json:"thread,omitempty"`
}
```

`Block` is a rich UI element (Slack Block Kit or Discord embed). The plugin serializes platform-specific blocks internally; the interface uses a generic representation.

```go
type Block struct {
    Type    string            `json:"type"`              // "section", "actions", "context", etc.
    Fields  map[string]any    `json:"fields,omitempty"`
    Elements []map[string]any `json:"elements,omitempty"`
}
```

## UserMessage Event

Each plugin publishes `UserMessage` events via the Event Bus on subject `foreman.plugin.<name>.message` for the Control Plane to consume:

```go
type UserMessage struct {
    Meta      EventMeta `json:"meta"`
    Plugin    string    `json:"plugin"`     // "slack", "discord"
    UserID    string    `json:"user_id"`
    Channel   string    `json:"channel"`
    Text      string    `json:"text"`
    ThreadTS  string    `json:"thread_ts,omitempty"`
    Raw       []byte    `json:"raw,omitempty"` // platform-specific original payload
}
```

## Responsibilities

- Receive natural language input from users
- Parse commands and context
- Publish `UserMessage` to Event Bus for Control Plane
- Subscribe to session events and report progress, approvals, results
- Support interactive patterns (thread replies, modals, buttons)

## Platform-Specific Notes

### Slack Plugin

- Slash commands (`/foreman review this PR`)
- App mentions (`@Foreman deploy the staging branch`)
- Interactive components (approve/deny buttons)
- Thread replies for long-running task updates
- Uses Slack Socket Mode (no public endpoint needed)

### Discord Plugin

- Slash commands
- Thread updates
- Role-based access control
