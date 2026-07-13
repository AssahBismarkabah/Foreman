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

- [X] E2E test via HTTP API: Docker Compose -> health -> submit task -> poll session -> COMPLETED

**Checkpoint:** Start Foreman, submit a task via HTTP API, see an agent work in a Docker container, get a result back. **MET** -- `TestE2E_ForemanStartTaskComplete` (in `test/e2e/e2e_test.go`) proves the full pipeline via Docker Compose, HTTP API, real Docker sandbox, exec adapter, and session COMPLETED.

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
- [X] Heartbeat monitoring -- goroutine per session, context cancellation on crash detection
- [X] Retry from checkpoint with exponential backoff with jitter
- [X] Enhanced Foreman recovery -- heartbeat age check, container reaper on bootstrap
- [X] Graceful shutdown with drain -- StopAccepting, Drain, 3-phase shutdown
- [X] Resource monitoring -- Stats() on DockerSandbox, threshold alerts with debounce
- [X] Integration tests -- crash detection+retry, graceful shutdown

**Progress:** All 7 phases (A-G) complete.

**Checkpoint:** Kill the Foreman mid-task, restart, verify the agent resumes from checkpoint. Kill the agent container, verify it gets restarted. **Met**: `TestCoordinatorCrashDetectionAndRetry` proves heartbeat detects crash, exponential backoff retries, retries exhaust -> FAILED. `TestCoordinatorGracefulShutdown` proves StopAccepting + Drain + clean teardown.

---

## Scale & Variety

Goal: Multiple sandbox types, multiple agent frameworks.

- [ ] Daytona sandbox provider
- [ ] AWS/ECS sandbox provider
- [ ] Claude Code adapter
- [ ] Codex adapter
- [ ] OpenHands adapter
- [X] Exec adapter (kind "exec", runs `sh -c <task>` in sandbox)
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
- [X] Admin API (POST /api/v1/tasks, GET /api/v1/sessions/{id})
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
- [X] 197 tests across 20 packages (18 with tests, all passing, `go vet` clean, `go build` clean, `golangci-lint` 0 issues):
  - `internal/adapter` -- JSONL parsing, BuildConfig, Verify, StartCommand, InjectPrompt
  - `internal/api` -- server start/shutdown, route registration, health endpoint
  - `internal/config` -- valid YAML, minimal YAML, missing file, invalid YAML
  - `internal/controlplane` -- create, transitions, emit, happy path, approval path
  - `internal/coordinator` -- submit, adapter failure, max concurrent, scoping, full pipeline events, multi-line, non-zero, provision failure, verify failure, integration, crash detection+retry, graceful shutdown
  - `internal/core` -- bootstrap memory bus, bootstrap Docker+OpenCode, invalid kinds, shutdown closes bus
  - `internal/eventbus` -- memory pub/sub, multiple subscribers, wildcards, no cross-talk, pub-before-sub; NATS embedded: pub/sub, wildcards, durable consumers
  - `internal/identity` -- config, model, keymanager (RSA/ECDSA), issuer (tokens, JWKS, OIDC, scoped tokens), middleware (auth, optional)
  - `internal/identity/githubapp` -- client auth, installation store, webhook routing + signature verification
  - `internal/mcphub` -- empty hub, with servers, resolve tools, register server, list servers
  - `internal/plugin` -- start/read output, send message, send block, stop kills process, name/version, done event
  - `internal/plugins/slack` -- New, Start validations, nil-client safety, toSlackBlock (section/actions/context/divider/unknown)
  - `internal/plugins/discord` -- New, Start validations, stop, toString, buttonStyle, nil-client safety
  - `internal/policy` -- compile configs, timeout, tool glob, wildcard glob, input regex, input field match, catch-all, multiple policies
  - `internal/sandbox` -- provision+destroy, execute, write+read file, exit code, subscribe events, destroy nonexistent, resource usage stats
  - `internal/schemas` -- Subject, SessionSubject, SessionEventSubject
   - `internal/statestore` -- create+get, update status, list non-terminal, not found, ping, description field, migrations, audit append, checkpoint save/get (5 tests)
   - `test/e2e` -- Docker Compose -> health -> HTTP task submit -> poll session -> COMPLETED (1 test)
