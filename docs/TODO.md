# Foreman Project Tracker

## Legend
- [X] done
- [ ] pending
- [~] in progress

---

## Foundation

Goal: Core daemon running with a single agent type and local Docker sandbox.

- [X] Go project scaffold (module structure, build, CI)
- [X] Core Service skeleton (config, startup, shutdown, health)
- [X] Config file parsing (foreman.yaml)
- [X] State Store schema + PostgreSQL connection
- [X] Event Bus (NATS + Memory with wildcards) integration
- [X] Control Plane with session state machine
- [X] Agent Coordinator with real Docker sandbox provisioning
- [X] One Agent Adapter (OpenCode with JSONL parsing)
- [X] MCP Hub with filesystem and git tools
- [X] Foreman CLI (serve + task subcommands)
- [ ] Session recovery on restart (happy path)
- [X] Integration test: coordinator -> real Docker -> mock adapter -> COMPLETED (checkpoint proof)

**Checkpoint:** Start Foreman, submit a task via CLI, see an agent work in a Docker container, get a result back. **MET** -- `TestIntegration_CoordinatorWithRealDocker` proves the full pipeline with real Docker sandbox, session COMPLETED, and container cleanup.

---

## Communication & Trust

Goal: Real chat interaction, approval gates, and identity.

- [ ] Slack Plugin (slash commands, thread updates, buttons)
- [ ] Discord Plugin (slash commands, thread updates)
- [ ] Approval Gate with policy engine
- [ ] Identity Provider + GitHub/GitLab App integration
- [ ] Scoped agent tokens
- [ ] Audit trail persistence
- [ ] Approval/deny flow through chat

**Checkpoint:** User writes `/foreman fix this bug` in Slack, agent fixes it, creates PR, user approves via button in Slack, PR is created.

---

## Reliability

Goal: Handle failures gracefully. Survive crashes. Retry smartly.

- [ ] Checkpoint system (periodic state snapshots)
- [ ] Agent crash detection + retry from checkpoint
- [ ] Foreman crash recovery (reload in-flight sessions)
- [ ] Retry policies with exponential backoff
- [ ] Resource monitoring (CPU/memory/disk alerts)
- [ ] Graceful shutdown with drain
- [ ] Integration tests for failure scenarios

**Checkpoint:** Kill the Foreman mid-task, restart, verify the agent resumes from checkpoint. Kill the agent container, verify it gets restarted.

---

## Scale & Variety

Goal: Multiple sandbox types, multiple agent frameworks.

- [ ] Daytona sandbox provider
- [ ] AWS/ECS sandbox provider
- [ ] Claude Code adapter
- [ ] Codex adapter
- [ ] OpenHands adapter
- [ ] Generic adapter (Dockerfile-based)
- [ ] Agent capability matching (pick the right agent for the job)
- [ ] Task routing (split complex tasks into sub-tasks)
- [ ] Concurrency limits and queuing

**Checkpoint:** Three agents running simultaneously in different sandboxes, each using a different agent framework, all reporting to the same Slack channel.

---

## Production Polish

Goal: Operable, documented, ready for real use.

- [ ] Monitoring & metrics (Prometheus + Grafana)
- [ ] Structured logging (JSON, levels, correlation IDs)
- [ ] Rate limiting per user/team/org
- [ ] Admin API (list sessions, cancel tasks, view logs)
- [ ] Configuration documentation
- [ ] Onboarding documentation
- [ ] Deployment guide (Docker Compose, Kubernetes)

---

## Completed Work

Tasks that fall outside the phase structure but are already done.

- [X] Competitive landscape research
- [X] Architecture document (docs/architecture.md)
- [X] PlantUML diagrams (docs/diagrams/)
- [X] Project README (README.md)
- [X] Project tracker (TODO.md)
- [X] API & landscape review (docs/api-review.md)
- [X] Hermes Agent research and assessment
- [X] Event message schemas defined (20+ typed Go structs)
- [X] Plugin interface defined (Name, Version, Start, Stop, SendMessage, SendBlockMessage)
- [X] AgentAdapter interface consolidated (section 3.6 -> 7.2 canonical)
- [X] Event serialization decided (JSON over NATS, with rationale)
- [X] NATS subject hierarchy designed (foreman.session/agent/approval/command/plugin.*)
- [X] Docker sandbox: provision, exec, file ops, heartbeat, logs, destroy
- [X] MemoryBus wildcard matching (NATS-style * and > patterns)
- [X] Coordinator wired to adapter + sandbox + MCP hub
- [X] CI pipeline: parallel lint/test jobs, module caching, golangci-lint v2, opencode cache
- [X] 107 tests total (all passing, golangci-lint clean):
  - adapter: 14 (JSONL parsing, BuildConfig, Verify, StartCommand, InjectPrompt)
  - config: 4 (valid YAML, minimal YAML, missing file, invalid YAML)
  - controlplane: 7 (create, transitions, emit, happy path, approval path)
  - coordinator: 8 (submit, adapter failure, max concurrent, scoping, full pipeline events, non-zero exit, provision failure, verify failure, multi-line output)
  - core: 5 (bootstrap memory bus, bootstrap Docker+OpenCode, invalid kinds, shutdown closes bus)
  - eventbus: 15 (10 memory + 5 NATS embedded: pub/sub, multiple subscribers, wildcards, no cross-talk, pub-before-sub)
  - mcphub: 6 (empty hub, with servers, resolve tools, register server, list servers)
  - plugin: 6 (start/read output, send message, send block, stop kills process, name/version, done event)
  - sandbox: 6 (provision+destroy, execute, write+read file, exit code, subscribe events, destroy nonexistent)
  - schemas: 3 (Subject, SessionSubject, SessionEventSubject)
