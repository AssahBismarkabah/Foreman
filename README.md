# Foreman

[![Go Report Card](https://goreportcard.com/badge/github.com/foreman/foreman)](https://goreportcard.com/report/github.com/foreman/foreman) [![Release](https://img.shields.io/github/v/release/AssahBismarkabah/Foreman?sort=semver)](https://github.com/AssahBismarkabah/Foreman/releases)

<img src="docs/logo.png" width="100">

----

Foreman is an open source orchestrator that connects team chat to isolated
agent sandboxes. It acts as a conductor for engineering work -- dispatch
tasks from Slack or Discord, provision ephemeral sandboxes, track progress,
gate destructive actions behind human approval, and get results back without
leaving your channel.

Foreman decides which agents (OpenCode, Claude Code, Codex, etc.) to use for
a given task, provisions isolated Docker sandboxes, tracks session progress,
gates destructive actions behind human approval, and reports results back to
the channel. Agents do the work. Foreman makes sure it is done right.

Foreman is hosted at [github.com/AssahBismarkabah/Foreman].

----

## To start using Foreman

See the [quickstart guide](docs/architecture.md) in the architecture doc.

```
docker compose -f deploy/docker-compose.yml --profile service up -d
```

To use Foreman code as a library in other applications, see the [list of
internal packages](internal/README.md). Use of the `github.com/foreman/foreman`
module or `github.com/foreman/foreman/...` packages as libraries is not
supported.

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
make docker-build
make docker-run
```

For the full story, head over to the [architecture documentation](docs/architecture.md).

## Support

If you need support, start with the [architecture documentation](docs/architecture.md),
and work your way through the process that we have outlined.

That said, if you have questions, reach out to us through [GitHub Issues].

## Governance

Foreman is governed by its maintainers. The [architecture document](docs/architecture.md)
describes the system design and the decision framework for contributions.

## Roadmap

The [TODO tracker](docs/TODO.md) covers the full roadmap across five phases:
Foundation, Communication & Trust, Reliability, Scale & Variety, and Production
Polish. Feature tracking and backlog are managed through [GitHub Issues].

[github.com/AssahBismarkabah/Foreman]: https://github.com/AssahBismarkabah/Foreman
[Go environment]: https://go.dev/doc/install
[Docker environment]: https://docs.docker.com/engine
[architecture documentation]: docs/architecture.md
[GitHub Issues]: https://github.com/AssahBismarkabah/Foreman/issues
