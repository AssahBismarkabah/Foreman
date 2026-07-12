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
- [X] Session recovery on restart (happy path)
- [X] Integration test: coordinator -> real Docker -> mock adapter -> COMPLETED (checkpoint proof)

**Checkpoint:** Start Foreman, submit a task via CLI, see an agent work in a Docker container, get a result back. **MET** -- `TestIntegration_CoordinatorWithRealDocker` proves the full pipeline with real Docker sandbox, session COMPLETED, and container cleanup.

---

## Communication & Trust

Goal: Real chat interaction, approval gates, and identity.

- [X] Slack Plugin (Socket Mode, slash commands, interactive buttons, DM handling)
- [X] Discord Plugin (Gateway API, slash commands, interactive buttons, DM handling)
- [X] Approval Gate with policy engine
- [X] Approval/deny flow through chat
- [X] Identity Provider -- config, model, keymanager, provider interface, OIDC issuer, JWT/JWKS, auth middleware
- [X] GitHub App integration -- client, installation store, webhook handler with signature verification
- [X] API Server -- HTTP wrapper with health, JWKS, OIDC configuration, and webhook routes
- [X] Builder wiring -- identity, API server, session recovery routing (RUNNING -> resume, APPROVAL -> cancel), plugin startup all wired in `internal/core/builder.go`
- [X] Scoped agent tokens -- AgentScope struct, structured claims, IssueScopedAgentToken, wired into coordinator (token in FOREMAN_AGENT_TOKEN env), default scope: read/pull
- [X] Audit trail persistence -- audit_log SQL table, AuditEntry struct + AppendAuditEntry in StateStore, wired into controlplane (session created/transition) and coordinator (adapter selection, sandbox provision, failures, approval request/grant/deny/timeout)

**Checkpoint:** User writes `/foreman fix this bug` in Slack, agent fixes it, creates PR, user approves via button in Slack, PR is created.

---

## Reliability

Goal: Handle failures gracefully. Survive crashes. Retry smartly.

- [X] Checkpoint infrastructure -- migration (000004), struct, interface, postgres impl, tests
- [X] Baseline checkpoint on agent start (coordinator runAgent)
- [ ] Periodic heartbeat monitoring + agent crash detection
- [ ] Retry from checkpoint with exponential backoff
- [ ] Enhanced Foreman recovery (heartbeat age check, container reaper)
- [ ] Graceful shutdown with drain
- [ ] Resource monitoring (CPU/memory/disk alerts)
- [ ] Integration tests for failure scenarios

**Progress:** Phase A complete (checkpoint migration, storage, baseline capture). Next: Phase B -- heartbeat monitoring with goroutine per session + context cancellation for crash detection.

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
- [X] 232 tests across 18 packages (all passing, `go vet` clean, `go build` clean, `golangci-lint` clean):
  - `internal/adapter` -- JSONL parsing, BuildConfig, Verify, StartCommand, InjectPrompt
  - `internal/api` -- server start/shutdown, route registration, health endpoint
  - `internal/config` -- valid YAML, minimal YAML, missing file, invalid YAML
  - `internal/controlplane` -- create, transitions, emit, happy path, approval path
  - `internal/coordinator` -- submit, adapter failure, max concurrent, scoping, full pipeline events, multi-line, non-zero, provision failure, verify failure, integration
  - `internal/core` -- bootstrap memory bus, bootstrap Docker+OpenCode, invalid kinds, shutdown closes bus
  - `internal/eventbus` -- memory pub/sub, multiple subscribers, wildcards, no cross-talk, pub-before-sub; NATS embedded: pub/sub, wildcards, durable consumers
  - `internal/identity` -- config, model, keymanager (RSA/ECDSA), issuer (tokens, JWKS, OIDC), middleware (auth, optional)
  - `internal/identity/githubapp` -- client auth, installation store, webhook routing + signature verification
  - `internal/mcphub` -- empty hub, with servers, resolve tools, register server, list servers
  - `internal/plugin` -- start/read output, send message, send block, stop kills process, name/version, done event
  - `internal/plugins/slack` -- New, Start validations, nil-client safety, toSlackBlock (section/actions/context/divider/unknown)
  - `internal/plugins/discord` -- New, Start validations, stop, toString, buttonStyle, nil-client safety
  - `internal/policy` -- compile configs, timeout, tool glob, wildcard glob, input regex, input field match, catch-all, multiple policies
  - `internal/sandbox` -- provision+destroy, execute, write+read file, exit code, subscribe events, destroy nonexistent
  - `internal/schemas` -- Subject, SessionSubject, SessionEventSubject
   - `internal/statestore` -- create+get, update status, list non-terminal, not found, ping, description field, migrations, audit append, checkpoint save/get (3 tests)
