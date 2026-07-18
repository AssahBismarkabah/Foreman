# Sandbox Reference

This document defines the Sandbox abstraction and types used by Foreman to execute agent workloads in isolated environments.

## Sandbox Interface

The sandbox is where agents execute. It must be isolated, disposable, and resource-constrained.

```go
type Sandbox interface {
    Provision(ctx context.Context, spec SandboxSpec) error
    Execute(ctx context.Context, cmd []string, env map[string]string) (*ExecutionResult, error)
    WriteFile(ctx context.Context, path string, content []byte) error
    ReadFile(ctx context.Context, path string) ([]byte, error)
    UploadCheckpoint(ctx context.Context, data []byte) error
    SubscribeEvents(ctx context.Context) (<-chan SandboxEvent, error)
    Heartbeat() (SandboxStatus, error)
    Destroy(ctx context.Context) error
}
```

## Shared Types

### SandboxType

```go
// SandboxType enumerates the supported sandbox implementations.
type SandboxType string

const (
    SandboxDocker      SandboxType = "docker"
    SandboxGVisor      SandboxType = "gvisor"
    SandboxKata        SandboxType = "kata"
    SandboxFirecracker SandboxType = "firecracker"
)
```

### SandboxSpec

```go
type SandboxSpec struct {
    Type         SandboxType       `json:"type"`
    Image        string            `json:"image"`                  // container image
    CPU          string            `json:"cpu,omitempty"`          // e.g. "2", "500m"
    Memory       string            `json:"memory,omitempty"`       // e.g. "4Gi", "512Mi"
    Disk         string            `json:"disk,omitempty"`         // e.g. "10Gi"
    Network      string            `json:"network,omitempty"`      // "isolated", "outbound", "full"
    Timeout      time.Duration     `json:"timeout,omitempty"`
    Env          map[string]string `json:"env,omitempty"`
    WorkDir      string            `json:"work_dir,omitempty"`
    RepoURL      string            `json:"repo_url,omitempty"`     // repo to clone on provision
    RepoBranch   string            `json:"repo_branch,omitempty"`
}
```

### ExecutionResult

```go
// ExecutionResult is returned by Sandbox.Execute.
type ExecutionResult struct {
    Stdout   string        `json:"stdout"`
    Stderr   string        `json:"stderr"`
    ExitCode int           `json:"exit_code"`
    Duration time.Duration `json:"duration,omitempty"`
}
```

### SandboxEvent

```go
// SandboxEvent is an event emitted by a running sandbox (log, status, error).
type SandboxEvent struct {
    Type      SandboxEventType `json:"type"`
    Data      string           `json:"data,omitempty"`
    Timestamp time.Time        `json:"timestamp"`
}

type SandboxEventType string

const (
    SandboxEventLog    SandboxEventType = "log"     // agent stdout/stderr line
    SandboxEventStatus SandboxEventType = "status"  // sandbox state change
    SandboxEventError  SandboxEventType = "error"   // sandbox-level error
    SandboxEventOOM    SandboxEventType = "oom"     // out of memory
)
```

### SandboxStatus

```go
// SandboxStatus is returned by Sandbox.Heartbeat.
type SandboxStatus struct {
    Alive       bool    `json:"alive"`
    CPUUsage    float64 `json:"cpu_usage,omitempty"`
    MemoryUsage float64 `json:"memory_usage,omitempty"`
    Uptime      string  `json:"uptime,omitempty"`
}
```

## Isolation Tiers

**Vendor lock-in decision:** We do NOT use third-party sandbox platforms (Daytona, Modal, E2B, Northflank) that charge per-use and create vendor dependency. Instead, we self-host using open-source isolation technology. The Sandbox interface is designed to abstract the provider -- switching from Docker to gVisor to Kata requires only a new implementation.

| Technology | Isolation | Cold Start | Memory Overhead | Syscall Compat | OCI Compat | Ops Complexity | Use Case |
|-----------|-----------|-----------|----------------|---------------|-----------|---------------|----------|
| **Docker (runc)** | Shared kernel (namespace) | <100ms | Negligible | Full | Native | Low | Dev, single-user |
| **gVisor (runsc)** | Userspace kernel | 1-10ms | 10-50MB | ~200 syscalls | Native | Low | Single-tenant production |
| **Kata + Firecracker** | Hardware VM (KVM) | 150-300ms | 50-150MB | Full (real kernel) | Native | Medium | Multi-tenant production |
| **Pure Firecracker** | Hardware VM (KVM) | 100-300ms | 5-10MB + guest kernel | Full (real kernel) | Not native | High | Max density/scale |

**Not recommended:** Building your own sandbox using Linux namespaces + cgroups + seccomp. Shared-kernel isolation (namespaces) is not a security boundary for untrusted agent code. Maintaining seccomp profiles is extremely tedious and breakage is silent. If you need more than Docker, use gVisor or Kata.

## Isolation Tiers (Detailed)

- **Tier 1 (Production, multi-tenant):** Kata Containers with Firecracker backend. Hardware-level VM isolation via KVM. OCI-compatible -- same `docker run` workflow with `--runtime=kata`. Open source (OpenInfra Foundation), no vendor lock-in. ~200ms cold start, full syscall compatibility.
- **Tier 2 (Production, single-tenant):** Docker + gVisor (runsc). Userspace kernel intercepts syscalls. ~1-10ms cold start, ~200 syscalls implemented. Simpler to operate than Kata but shares host kernel.
- **Tier 3 (Development):** Plain Docker (runc). Shared kernel, sufficient for single-user dev. Zero additional setup.

## Resource Limits

```yaml
sandbox_defaults:
  cpu: "2"
  memory: "4Gi"
  disk: "10Gi"
  network: "isolated"  # isolated, outbound-only, full
  timeout: "30m"        // max agent runtime

sandbox_overrides:
  - task_type: "code_review"
    cpu: "1"
    memory: "2Gi"
    timeout: "10m"
  - task_type: "deploy"
    timeout: "5m"
    network: "outbound-only"
```

## Workspace Layout

A provisioned sandbox exposes the following paths under `/workspace/`:

- `/workspace/repo/` -- cloned repository
- `/workspace/.foreman/config.yaml` -- agent-specific config
- `/workspace/.foreman/checkpoint.json` -- latest checkpoint
- `/workspace/.foreman/logs/` -- agent output logs
- `/workspace/.mcp-servers/` -- MCP server sockets and configs
