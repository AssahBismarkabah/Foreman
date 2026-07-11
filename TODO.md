# Foreman Project Tracker

## Legend
- [X] done
- [ ] pending
- [~] in progress

---

## Foundation

Goal: Core daemon running with a single agent type and local Docker sandbox.

- [ ] Go project scaffold (module structure, build, CI)
- [ ] Core Service skeleton (config, startup, shutdown, health)
- [ ] Config file parsing (foreman.yaml)
- [ ] State Store schema + PostgreSQL connection
- [ ] Event Bus (NATS) integration
- [ ] Control Plane with session state machine
- [ ] Agent Coordinator with local Docker sandbox provisioning
- [ ] One Agent Adapter (OpenCode, as the first target)
- [ ] MCP Hub with filesystem and git tools
- [ ] Foreman CLI (serve + task subcommands)
- [ ] Session recovery on restart (happy path)

**Checkpoint:** Start Foreman, submit a task via CLI, see an agent work in a Docker container, get a result back.

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
