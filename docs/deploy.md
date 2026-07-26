# Deploying Foreman

Foreman is an orchestrator that connects team chat to isolated agent sandboxes.
You run Foreman wherever you want -- a VM, a container, your laptop -- and it
provisions sandboxes for agents to work in.

The [system context diagram](./diagrams/system-context.puml) shows the
high-level architecture.

---

## How It Works

Foreman sits between chat and agents:

See the [deployment overview diagram](./diagrams/deployment-overview.puml) for
the full picture.

- You talk to Foreman via slash commands in Discord or Slack
- Foreman provisions an isolated sandbox for each task
- The agent runs inside the sandbox with MCP tools
- Results come back to your chat channel

---

## Running Foreman

Foreman is distributed as a Docker image on GitHub Container Registry. You can
run it on any machine that has Docker -- a cloud VM, a VPS, a Raspberry Pi, or
your development laptop.

### Quick start

```bash
docker run -d \
  --name foreman \
  -p 8080:8080 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e FOREMAN_PG_DSN="postgresql://..." \
  -e FOREMAN_SIGNING_KEY="$(openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -outform PEM | base64 -w0)" \
  -e DISCORD_BOT_TOKEN="your-discord-token" \
  -e OPENAI_API_KEY="your-api-key" \
  -e OPENAI_BASE_URL="https://api.opencode.ai/v1" \
  -e OPENAI_MODEL="opencode/deepseek-v4-flash-free" \
  ghcr.io/assahbismarkabah/foreman:latest
```

> The Docker socket (`/var/run/docker.sock`) is mounted so Foreman can
> provision sandbox containers. This is the default Docker sandbox provider.

### Using Docker Compose

A [docker-compose.yml](../deploy/docker-compose.yml) is provided. It starts
PostgreSQL and Foreman together:

```bash
docker compose -f deploy/docker-compose.yml up -d
```

Set environment variables in a `.env` file (see `foreman.yaml` for options).

### Platform-specific deployment

Because Foreman is a single Docker image, it runs on any platform that supports
Docker:

| Platform | Notes |
|---|---|
| **Any cloud VM** (EC2, DigitalOcean, Linode, Hetzner) | Standard Docker setup |
| **Kubernetes** | Deploy as a Deployment with env vars from a Secret |
| **Railway / Render / Fly.io** | Use the Docker deploy option |
| **Your laptop** | Docker Desktop or OrbStack |

The [infra/](../infra/README.md) directory shows how this project deploys
Foreman on AWS EC2 as a reference example.

---

## Sandbox Providers

Foreman provisions sandboxes for agents to work in. The sandbox is where the
agent runs (installs dependencies, edits files, executes commands).

### Docker (default)

The built-in sandbox provider creates ephemeral Docker containers. Foreman
manages the full lifecycle: provision, execute commands, read results, destroy.

- Agent image: `node:20-bookworm` with `opencode-ai` pre-installed
- Fully isolated per task
- Destroyed automatically when the task completes

### Other sandbox types

Foreman supports multiple sandbox backends through the Sandbox interface.
Additional providers can be added:

- **Daytona** -- remote dev environments
- **AWS ECS** -- Fargate tasks
- **Exec** -- runs commands directly on the host (development only)

Set the sandbox type in `foreman.yaml`:

```yaml
sandbox:
  kind: docker        # docker (default), daytona, ecs, exec
```

See the [Sandbox Reference](./reference/sandbox.md) for details.

---

## Prerequisites

### PostgreSQL

Any PostgreSQL 15+ database, hosted anywhere. Options:

- [Neon](https://neon.tech) (free tier)
- AWS RDS, DigitalOcean Managed DB, any self-hosted Postgres

Connection string format:

```
postgresql://user:password@host:5432/dbname?sslmode=require
```

### Discord Bot (optional)

1. Create an application at https://discord.com/developers/applications
2. Enable **Bot** > **Message Content Intent**
3. Copy the bot token
4. Use the OAuth2 URL Generator with `bot` and `applications.commands` scopes
   to invite the bot to your server

### Slack App (optional)

1. Create an app at https://api.slack.com/apps
2. Enable **Socket Mode**
3. Add `commands` and `chat:write` bot token scopes
4. Install to workspace. Copy the Bot Token (`xoxb-*`) and App-Level Token
   (`xapp-*`)

### LLM API Key

Agents need an OpenAI-compatible API to respond to tasks. Options:

- **OpenCode Zen (free):** `https://api.opencode.ai/v1`, model
  `opencode/deepseek-v4-flash-free`
- **Any OpenAI-compatible provider:** set `OPENAI_BASE_URL`, `OPENAI_API_KEY`,
  and the model name the provider supports (e.g. `gpt-4o`, `deepseek-v4-flash`)

---

## Configuration

Foreman is configured through environment variables (for the Docker image) or
`foreman.yaml` (for the binary).

### Environment Variables

| Variable | Required | Description |
|---|---|---|
| `FOREMAN_PG_DSN` | Yes | PostgreSQL connection string |
| `FOREMAN_SIGNING_KEY` | Yes | Base64-encoded RSA private key for JWT. Generate: `openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -outform PEM \| base64 -w0` |
| `DISCORD_BOT_TOKEN` | One of | Discord bot token |
| `SLACK_BOT_TOKEN` | One of | Slack bot token (`xoxb-*`) |
| `SLACK_APP_TOKEN` | One of | Slack app-level token (`xapp-*`) |
| `OPENAI_API_KEY` | Yes* | LLM provider API key |
| `OPENAI_BASE_URL` | Yes* | LLM provider base URL |
| `OPENAI_MODEL` | Yes* | Model name (e.g. `opencode/deepseek-v4-flash-free`) |

(\* Required for agent sandboxes to make LLM calls.)

### foreman.yaml

When running the binary directly, create a `foreman.yaml`:

```yaml
eventbus:
  kind: memory

sandbox:
  kind: docker
  image: node:20-bookworm

adapter:
  kind: opencode
  command: npx opencode

plugins:
  discord:
    token: "${DISCORD_BOT_TOKEN}"

state_store:
  dsn: "${FOREMAN_PG_DSN}"
```

---

## Verifying It Works

Health check:

```bash
curl http://localhost:8080/healthz
# {"status":"ok"}
```

In Discord, type `/foreman hello`. Foreman provisions a sandbox, runs the
agent, and responds back in the channel.

---

## Upgrading

```bash
docker pull ghcr.io/assahbismarkabah/foreman:latest
docker stop foreman && docker rm foreman
docker run ... ghcr.io/assahbismarkabah/foreman:latest
```

Database migrations run automatically on startup.

---

## Further Reading

- [Architecture Overview](./architecture.md)
- [System Context Diagram](./diagrams/system-context.puml)
- [Sandbox Reference](./reference/sandbox.md) -- supported providers and configuration
- [Agent Adapter Reference](./reference/agent-adapter.md)
- [Communication Plugin Reference](./reference/communication-plugin.md)
- [Event Bus Reference](./reference/event-bus.md)
- [MCP Hub Reference](./reference/mcp-hub.md)
- [Roadmap & Tracker](./TODO.md)
- [Infrastructure Reference](../infra/README.md) -- how this project runs Foreman in production
