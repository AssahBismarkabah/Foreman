# Foreman

Foreman is an open source orchestrator that connects team chat to isolated
agent sandboxes. It dispatches tasks from Slack or Discord, provisions ephemeral
sandboxes, tracks session progress, gates destructive actions behind human
approval, and reports results back to the channel -- without leaving your chat.

Foreman decides which agents ([OpenCode](https://opencode.ai), Claude Code,
Codex, etc.) to use for a given task, provisions the right sandbox environment
(Docker, Daytona, ECS, etc.), and makes sure the work is done right.

## Help and Documentation

- [Deployment Guide](docs/deploy.md) - how to set up and configure Foreman
- [Architecture Documentation](docs/architecture.md) - how Foreman is designed
  and built
- [Roadmap](docs/TODO.md) - the planned work across all five phases
- [GitHub Issues](https://github.com/AssahBismarkabah/Foreman/issues) - report
  bugs and track planned features

## Reporting Security Vulnerabilities

If you have found a security vulnerability, please report it privately through
the [security advisories page](https://github.com/AssahBismarkabah/Foreman/security)
instead of opening a public issue.

## Reporting an issue

If you believe you have discovered a defect in Foreman, please open
[an issue](https://github.com/AssahBismarkabah/Foreman/issues).
Please remember to provide a good summary, description as well as steps to
reproduce the issue.

## Getting started

The quickest way to run Foreman is the Docker image on GitHub Container
Registry. See the [deployment guide](docs/deploy.md) for full setup, or run:

```
docker run -d \
  --name foreman \
  -p 8080:8080 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e FOREMAN_PG_DSN="postgresql://..." \
  -e FOREMAN_SIGNING_KEY="$(openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -outform PEM | base64 -w0)" \
  -e DISCORD_BOT_TOKEN="your-discord-token" \
  ghcr.io/assahbismarkabah/foreman:latest
```

For local development, clone the repository and use the Makefile targets:

```
git clone https://github.com/AssahBismarkabah/Foreman
cd Foreman
make up          # start PostgreSQL via Docker Compose
make wait-db     # wait for DB to be ready
make test        # run tests
make build       # build binary
```

For more details refer to the [deployment guide](docs/deploy.md).

## Building from Source

Foreman is written in Go. To build from source, refer to the
[architecture documentation](docs/architecture.md), which hosts all information
about building Foreman from source and how the system is designed.

If you want to build Foreman right away there are two options:

##### You have a working [Go environment]

```
git clone https://github.com/AssahBismarkabah/Foreman
cd Foreman
make build
make test
```

##### You have a working [Docker environment]

```
git clone https://github.com/AssahBismarkabah/Foreman
cd Foreman
make docker
make up
```

### Testing

To run tests, use the Makefile targets:

```
make test         # all tests (unit + integration)
make test-unit    # unit tests only, no Docker or PostgreSQL required
```

## Contributing

Contributions are welcome. Open a pull request against the
[Foreman repository](https://github.com/AssahBismarkabah/Foreman), and see the
[architecture documentation](docs/architecture.md) for how to build from source
and how the system is designed. Planned work and feature tracking are managed
through [GitHub Issues](https://github.com/AssahBismarkabah/Foreman/issues).

## License

- [Apache License, Version 2.0](https://www.apache.org/licenses/LICENSE-2.0)

[Go environment]: https://go.dev/doc/install
[Docker environment]: https://docs.docker.com/engine
