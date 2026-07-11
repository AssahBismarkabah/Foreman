# API & Landscape Review

## 1. Hermes Agent

**What it is:** Open-source self-improving AI agent from Nous Research. Runs persistently on a server, has a built-in learning loop, persistent memory, skill creation system, MCP integration, and multi-channel messaging gateway (Slack, Discord, Telegram, WhatsApp, Signal, Email, CLI).

24,600+ GitHub stars in eight weeks.

**What it is NOT:** It is not an orchestrator or foreman. It is an agent -- like Claude Code or OpenCode, but broader (not just coding).

**Relevance to Foreman:**

| Feature | Overlap with Foreman | Assessment |
|---------|---------------------|------------|
| Multi-channel gateway (Slack, Discord) | Foreman also needs this | They built it. We would build our own in Go. No code reuse possible (Python vs Go) but architecture reference is useful. |
| MCP integration | Foreman MCP Hub | They connect to MCP servers and filter tools. Our architecture describes the same pattern. |
| Profiles (isolated agent instances) | Foreman sandboxing | They have a profiles feature for isolation. Our approach (per-task sandbox) is different. |
| Memory system (3-layer) | Foreman checkpoints | Their memory model is interesting but focused on cross-session learning, not crash recovery. Different use case. |
| Skills system | Not in Foreman scope | Skills = procedural memory the agent creates and reuses. Useful for an agent, not for the orchestrator. |

**Verdict:** Hermes Agent is not code we can reuse (Python vs Go) and solves a different problem (being an agent vs orchestrating agents). However, reading its architecture is recommended before we build our Slack plugin and MCP Hub -- they have production patterns we should learn from. Add a task to review their messaging gateway and MCP integration architecture.

---

## 2. Agent Sandboxing Landscape (2026)

**Key finding: Docker alone is not enough.** Multiple CVEs in 2025 (Langflow CVE-2025-3248, CVSS 9.8) confirmed that shared-kernel container isolation is insufficient for AI agent code execution. Agents generate and execute arbitrary code at runtime -- that code was not reviewed by engineers.

**Isolation tiers available in 2026:**

| Technology | Isolation Level | Cold Start | Use Case |
|-----------|----------------|------------|----------|
| Docker/runc | Shared kernel | <100ms | Dev, single-user, low trust |
| gVisor | User-space kernel | 200-500ms | Production agent workloads |
| Firecracker microVM | Hardware VM | 500ms-1s | Multi-tenant, untrusted code |
| Kata Containers | Lightweight VM | 1-2s | Enterprise, compliance |

**Managed platforms we could leverage instead of building sandbox infrastructure:**

| Platform | Isolation | Cost | Pros | Cons |
|----------|-----------|------|------|------|
| **Daytona** | gVisor | $0.05/vCPU-hr | SDK-managed, warm sandboxes, lifecycle automation | gVisor only, no GPU |
| **Modal** | gVisor | Per-second | GPU support, 50K+ concurrent, fast cold starts | Full platform dependency |
| **E2B** | MicroVM | $0.05/vCPU-hr | Purpose-built for AI agents, OSS option | Smaller ecosystem |
| **Northflank** | Kata/Firecracker/gVisor | Variable | Multiple isolation choices, own VPC | More complex |

**Verdict:** Our current architecture says "Local Docker for dev, Daytona for remote workspaces, AWS for production." This is still valid as a phased approach, but we need to add gVisor as a recommended isolation layer on top of Docker for production. We should NOT build our own sandbox infrastructure -- leverage Daytona for remote/multi-tenant and gVisor-wrapped Docker for local single-tenant.

---

## 3. API Completeness Review

### 3.1 Defined Interfaces

| Interface | Section | Methods | Status |
|-----------|---------|---------|--------|
| `AgentAdapter` | 7.2 | 10 methods | Complete. Covers metadata, lifecycle, communication, health. |
| `Sandbox` | 9.1 | 8 methods | Complete. Covers provision, execute, file ops, checkpoint, events, heartbeat, destroy. |

### 3.2 Missing Interfaces Needed Before Implementation

The architecture defines components but does not define how they talk to each other. These are the gaps:

| Missing | Who Needs It | Priority | Notes |
|---------|-------------|----------|-------|
| **Event message schemas** | Control Plane, Coordinator, Plugins | Critical | Events are listed as strings (`session.created`, `agent.heartbeat`) but have no payload definitions. Need typed event structs before building Event Bus integration. |
| **Plugin API** | Slack, Discord plugins | Critical | How does a plugin register with Foreman? How does it send events to the Control Plane? Need `Plugin` interface before Phase 1 can start. |
| **Control Plane API** | Coordinator, Plugins | High | The Control Plane is the central hub but has no defined method surface. How does the Coordinator report status? How does a plugin submit a task? |
| **Coordinator API** | Control Plane | High | The Control Plane dispatches to the Coordinator via Event Bus. The event contract needs to be defined: what does an `allocate` command look like? |
| **MCP Hub API** | Coordinator | Medium | How does the Coordinator ask the Hub for tool bindings? A method like `ResolveTools(capabilities []Capability) []ToolBinding` is missing. |
| **State Store interface** | All components | Medium | Schema domains are listed but there is no repository interface. Each component needs a defined data access contract. |

### 3.3 Inconsistencies

1. **Dual AgentAdapter interfaces** -- Section 3.6 has a simpler version (6 methods), Section 7.2 has the full version (10 methods). The 3.6 version is outdated and should be removed or updated to reference 7.2.

2. **Event Bus is listed as technology choice (NATS)** but the event schema and component contracts are not defined. We need to decide: are events Go structs sent over NATS? Protobuf? JSON? This decision affects everything.

---

## 4. Recommendations Before Implementation

### Must fix before writing code:

1. **Define event message schemas** -- Convert the event list (section 3.3) into typed Go structs with payloads. Example:
   ```go
   type SessionCreated struct {
       SessionID string
       UserID    string
       Plugin    string
       Task      string
       Timestamp time.Time
   }
   ```

2. **Define Plugin interface** -- The plugin system needs a contract before Phase 1:
   ```go
   type Plugin interface {
       Name() string
       Start(ctx context.Context, bus EventBus) error
       Stop(ctx context.Context) error
   }
   ```

3. **Consolidate AgentAdapter interfaces** -- Remove the section 3.6 version, keep only the section 7.2 version as canonical.

### Worth discussing before implementation:

4. **Sandbox isolation model** -- Should we go beyond Docker? For Foundation phase, Docker is fine. But the architecture doc should acknowledge gVisor as a future upgrade path.

5. **Hermes Agent review task** -- Add a TODO item to read Hermes Agent's messaging gateway and MCP integration architecture before building our own.

6. **Event serialization format** -- Go structs over NATS (using `encoding/gob` or `encoding/json`) vs Protobuf. Go structs are simpler initially, Protobuf helps if we need other language clients later.
