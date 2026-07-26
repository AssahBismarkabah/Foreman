# Foreman

[![Release](https://img.shields.io/github/v/release/AssahBismarkabah/Foreman?sort=semver)](https://github.com/AssahBismarkabah/Foreman/releases)
[![CI](https://img.shields.io/github/actions/workflow/status/AssahBismarkabah/Foreman/ci.yml?branch=main&label=CI&logo=github)](https://github.com/AssahBismarkabah/Foreman/actions/workflows/ci.yml)
[![Infra](https://img.shields.io/github/actions/workflow/status/AssahBismarkabah/Foreman/infra.yml?branch=main&label=Infra&logo=githubactions)](https://github.com/AssahBismarkabah/Foreman/actions/workflows/infra.yml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/AssahBismarkabah/Foreman?logo=go)](https://go.dev/doc/install)
[![Last Commit](https://img.shields.io/github/last-commit/AssahBismarkabah/Foreman?label=updated)](https://github.com/AssahBismarkabah/Foreman/commits/main)

---
Foreman is an open source orchestrator that connects team chat to isolated
agent sandboxes. It acts as a conductor for engineering work -- dispatch
tasks from Slack or Discord, provision ephemeral sandboxes, track progress,
gate destructive actions behind human approval, and get results back without
leaving your channel.

Foreman decides which agents (OpenCode, Claude Code, Codex, etc.) to use for
a given task, provisions isolated Docker sandboxes, tracks session progress,
gates destructive actions behind human approval, and reports results back to
the channel. Agents do the work. Foreman makes sure it is done right.

---

## To start using Foreman

See the [deployment guide](docs/deploy.md) for setup and configuration.

For local development:

```bash
make up        # start PostgreSQL via Docker Compose
make wait-db   # wait for DB to be ready
make test      # run tests
make build     # build binary
```

## To start developing Foreman

The [architecture documentation](docs/architecture.md) hosts all information
about building Foreman from source, how to contribute code and documentation,
who to contact about what, etc.

If you want to build Foreman right away there are two options:

##### You have a working [Go environment].

```
git clone https://github.com/AssahBismarkabah/Foreman
cd Foreman
make build
make test
```

##### You have a working [Docker environment].

```
make docker
make up
```

## Documentation

| Document | Description |
|---|---|
| [Deployment Guide](docs/deploy.md) | Deploy Foreman on AWS in ~10 minutes |
| [Architecture](docs/architecture.md) | System design, components, and interfaces |
| [Reference Docs](docs/reference/) | Agent adapters, plugins, event bus, sandbox, MCP hub |
| [Roadmap](docs/TODO.md) | Project tracker across all phases |
| [Infrastructure README](infra/README.md) | Terraform + Ansible details |

## Support

If you have questions, reach out through [GitHub Issues].

## Governance

Foreman is governed by its maintainers. The [architecture document](docs/architecture.md)
describes the system design and the decision framework for contributions.

[github.com/AssahBismarkabah/Foreman]: https://github.com/AssahBismarkabah/Foreman
[Go environment]: https://go.dev/doc/install
[Docker environment]: https://docs.docker.com/engine
[GitHub Issues]: https://github.com/AssahBismarkabah/Foreman/issues
