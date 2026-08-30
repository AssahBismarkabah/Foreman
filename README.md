# Foreman

Foreman is an open source orchestrator that connects team chat to isolated
agent sandboxes. It dispatches tasks from Slack or Discord, provisions ephemeral
sandboxes, tracks session progress, gates destructive actions behind human
approval, and reports results back to the channel -- without leaving your chat.
Foreman decides which agents ([OpenCode](https://opencode.ai), Claude Code,
Codex, etc.) to use for a given task, provisions the right sandbox environment
(Docker, Daytona, ECS, etc.), and makes sure the work is done right.

## Help and Documentation

- [Deployment Guide](docs/deploy.md)
- [Architecture Documentation](docs/architecture.md)

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

Run Foreman with Docker:

```
docker run -d \
  --name foreman \
  -p 8080:8080 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e FOREMAN_PG_DSN="postgresql://..." \
  -e FOREMAN_SIGNING_KEY="$(openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -outform PEM | base64 -w0)" \
  ghcr.io/assahbismarkabah/foreman:latest
```

For more details refer to the [deployment guide](docs/deploy.md).

## Building from Source

Foreman is written in Go.

```
git clone https://github.com/AssahBismarkabah/Foreman
cd Foreman
make build       # build binary
make test
```

## Contributing

Contributions are welcome. Open a pull request against the
[Foreman repository](https://github.com/AssahBismarkabah/Foreman).

## License

- [Apache License, Version 2.0](https://www.apache.org/licenses/LICENSE-2.0)
