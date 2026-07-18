# MCP Hub Reference

This document defines the Model Context Protocol (MCP) Hub and Tool Registry used by Foreman to bind tools to agents.

## Purpose

The Model Context Protocol is how agents get access to tools (git, filesystem, search, etc.). The MCP Hub manages which tools are available and binds them to agents at spawn time.

## Tool Registry

Static registry of available MCP servers:

```yaml
mcp_servers:
  - name: "filesystem"
    command: "npx"
    args: ["-y", "@modelcontextprotocol/server-filesystem"]
    allowed_paths: ["/workspace"]
    capabilities: [CapFileRead, CapFileWrite]

  - name: "github"
    command: "npx"
    args: ["-y", "@modelcontextprotocol/server-github"]
    auth_method: "token"
    capabilities: [CapGitRead, CapGitWrite, CapPRCreate]

  - name: "test-runner"
    command: "npx"
    args: ["-y", "@modelcontextprotocol/server-shell"]
    allowed_commands: ["npm test", "pnpm test", "cargo test", "go test", "pytest"]
    capabilities: [CapTestRun]
```

## Types

`ToolBinding` describes an MCP server that should be available to an agent. Produced by the MCP Hub, consumed by `AgentAdapter.BuildConfig`.

```go
type ToolBinding struct {
    ServerName   string      `json:"server_name"`
    Transport    string      `json:"transport"`            // "stdio" or "sse"
    Command      string      `json:"command,omitempty"`    // for stdio servers
    Args         []string    `json:"args,omitempty"`
    URL          string      `json:"url,omitempty"`        // for SSE/HTTP servers
    AllowedTools []string    `json:"allowed_tools,omitempty"`
}
```

`MCPServerConfig` is the runtime representation of a registered MCP server. It maps to the YAML tool registry schema.

```go
type MCPServerConfig struct {
    Name        string   `json:"name"`
    Command     string   `json:"command"`
    Args        []string `json:"args,omitempty"`
    AuthMethod  string   `json:"auth_method,omitempty"`
    Capabilities []Capability `json:"capabilities,omitempty"`
    AllowedPaths  []string `json:"allowed_paths,omitempty"`
    AllowedCommands []string `json:"allowed_commands,omitempty"`
}
```

`MCPHub` is the interface for the tool registry component.

```go
type MCPHub interface {
    // ResolveTools selects MCP servers matching the requested capabilities.
    ResolveTools(ctx context.Context, caps []Capability) ([]ToolBinding, error)
    // RegisterServer adds an MCP server to the registry.
    RegisterServer(ctx context.Context, config MCPServerConfig) error
    // ListServers returns all registered MCP servers.
    ListServers(ctx context.Context) ([]MCPServerConfig, error)
}
```

## Dynamic Binding

When an agent is spawned:
1. Control Plane determines required capabilities from the task spec
2. MCP Hub selects the subset of servers that provide those capabilities
3. Coordinator provisions the sandbox with those MCP servers running alongside the agent
4. Agent discovers the MCP servers via the MCP protocol (stdio or SSE)

## MCP Server Lifecycle

Managed by the Agent Coordinator within each sandbox:
- **Start:** Before the agent launches, start the required MCP servers
- **Bind:** Configure the agent to connect to them (via stdio sockets or SSE endpoints)
- **Monitor:** Track MCP server health alongside the agent
- **Stop:** On sandbox teardown, gracefully shut down all MCP servers
- **Log:** Capture MCP server logs for debugging

## Protocol Version Support

MCP has two active protocol versions. The Hub must support both:

**Legacy (2025-11-25) -- Session-based:**
- Requires `initialize` handshake before operation: exchange protocol versions, capabilities, client/server info
- Client sends `notifications/initialized` to complete handshake
- Session state maintained over transport connection
- Transport: stdio subprocess or HTTP+SSE

**Modern (2026-07-28) -- Stateless:**
- No handshake required; each request is self-contained
- Protocol version and capabilities carried in `_meta` field on every request
- Optional `server/discover` RPC for capability probing
- Cancellation: close SSE response stream (HTTP) or `notifications/cancelled` (stdio)
- Transport: stdio subprocess or Streamable HTTP (single endpoint, POST only)

### Go SDKs

| SDK | Version | Maturity | Protocol | Use For |
|-----|---------|----------|----------|---------|
| `mark3labs/mcp-go` | v0.31+ | 2500+ stars, MIT | 2025-11-25 | Initial Hub implementation |
| `modelcontextprotocol/go-sdk` | v1.1+ | Official (Google) | 2026-07-28 | Modern protocol upgrade path |

### Version Detection

The Hub probes a server with `server/discover`. If the call succeeds, the
server speaks the Modern (2026-07-28) protocol. If it returns
`MethodNotFound` or `UnsupportedProtocolVersionError`, fall back to the
`initialize` handshake for the Legacy (2025-11-25) protocol. Cache the
detected era per server endpoint.

### Transport Handling

- **stdio (sandbox-managed servers):** Hub spawns the MCP server as a subprocess inside the sandbox, writes JSON-RPC to stdin, reads responses from stdout. Server logs go to stderr. On teardown: close stdin, SIGTERM, SIGKILL if needed.
- **Streamable HTTP (external servers):** Each request is an HTTP POST to the server's endpoint. For external servers, the Hub manages OAuth 2.1 with PKCE authentication and token refresh.
