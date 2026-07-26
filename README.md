# Foreman

[![Release](https://img.shields.io/github/v/release/AssahBismarkabah/Foreman?sort=semver)](https://github.com/AssahBismarkabah/Foreman/releases)
[![CI](https://img.shields.io/github/actions/workflow/status/AssahBismarkabah/Foreman/ci.yml?branch=main&label=CI&logo=github)](https://github.com/AssahBismarkabah/Foreman/actions/workflows/ci.yml)
[![Infra](https://img.shields.io/github/actions/workflow/status/AssahBismarkabah/Foreman/infra.yml?branch=main&label=Infra&logo=githubactions)](https://github.com/AssahBismarkabah/Foreman/actions/workflows/infra.yml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/AssahBismarkabah/Foreman?logo=go)](https://go.dev/doc/install)
[![Last Commit](https://img.shields.io/github/last-commit/AssahBismarkabah/Foreman?label=updated)](https://github.com/AssahBismarkabah/Foreman/commits/main)

----

Foreman is an open source orchestrator that connects team chat to isolated
agent sandboxes. It dispatches tasks from Slack or Discord, provisions ephemeral
sandboxes, tracks session progress, gates destructive actions behind human
approval, and reports results back to the channel -- without leaving your chat.

Foreman decides which agents ([OpenCode](https://opencode.ai), Claude Code,
Codex, etc.) to use for a given task, provisions the right sandbox environment
(Docker, Daytona, ECS, etc.), and makes sure the work is done right.

----

## To start using Foreman

See the [deployment guide](docs/deploy.md) for setup and configuration using
the Docker image.

For local development:

```bash
git clone https://github.com/AssahBismarkabah/Foreman
cd Foreman
make up          # start PostgreSQL via Docker Compose
make wait-db     # wait for DB to be ready
make test        # run tests
make build       # build binary
```

## To start developing Foreman

The [architecture documentation](docs/architecture.md) hosts all information
about building Foreman from source, how to contribute code and documentation,
and how the system is designed.

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
git clone https://github.com/AssahBismarkabah/Foreman
cd Foreman
make docker
make up
```

For the full story, head over to the [architecture documentation](docs/architecture.md).

## Support

If you need support, start with the [deployment guide](docs/deploy.md).
If you have questions, reach out through [GitHub Issues].

## Governance

Foreman is governed by its maintainers. The [architecture document](docs/architecture.md)
describes the system design and the decision framework for contributions.

## Roadmap

The [TODO tracker](docs/TODO.md) covers the full roadmap across five phases:
Foundation, Communication & Trust, Reliability, Scale & Variety, and Production
Polish. Feature tracking and backlog are managed through [GitHub Issues].

[GitHub Issues]: https://github.com/AssahBismarkabah/Foreman/issues
[Go environment]: https://go.dev/doc/install
[Docker environment]: https://docs.docker.com/engine
