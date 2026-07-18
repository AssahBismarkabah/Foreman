# Foreman Architecture

This directory documents the architecture of the Foreman orchestrator. The
content here is the authoritative source for how Foreman is structured and
how components interact.

Architecture documents currently staged here:

- [`architecture.md`](./architecture.md) - This file. High-level overview of Foreman's architecture.
- [`reference/event-bus.md`](./reference/event-bus.md) - Event Bus interface, subject hierarchy, and event schemas.
- [`reference/agent-adapter.md`](./reference/agent-adapter.md) - Agent Adapter interface and shared types.
- [`reference/communication-plugin.md`](./reference/communication-plugin.md) - Communication Plugin interface and message types.
- [`reference/mcp-hub.md`](./reference/mcp-hub.md) - MCP Hub interface, tool registry, and protocol support.
- [`reference/sandbox.md`](./reference/sandbox.md) - Sandbox interface, types, and isolation tiers.

The documents in `docs/` are authoritative, i.e. the only copy of the
architecture documentation. You can directly modify such documents.

## Using architecture documents from Foreman code

Foreman code uses the interfaces and types documented here as the source of
truth for component contracts. For example, when a component publishes a
session event, it uses the subject and schema defined in
[`reference/event-bus.md`](./reference/event-bus.md):

```go
// internal/controlplane/session.go
package controlplane

import (
    "github.com/foreman/foreman/internal/eventbus"
)

func (s *Service) emitSessionCreated(ctx context.Context, evt eventbus.SessionCreated) error {
    return s.bus.Publish(ctx, "foreman.session.created", evt)
}
```

## Creating a new architecture document

### Adding the document to `docs/`:

1. Determine which component or contract the document covers.
2. Create the new file under `docs/reference/` if it defines an API, interface,
   or schema; otherwise place it directly under `docs/`.
3. Update this `architecture.md` to add the document to the list above.
4. Link to the new document from the relevant component section or overview.
5. Keep the document focused on one concern. Interface definitions, event
   schemas, and examples belong in reference documents; high-level concepts and
   design decisions belong in this file.

### Updating the published documentation

1. Ensure the new document follows the same plain, link-heavy style as the
   existing documents.
2. Run `go test ./...` and any documentation linters to verify that linked
   paths and code samples remain valid.
3. Submit a pull request with the changes. Architecture documentation changes
   are reviewed alongside code changes.
