# Foreman

Run coding agents from your chat, with no infrastructure to manage. No need to
provision sandboxes, wire up agents, or babysit approvals yourself.

Foreman dispatches tasks from Slack or Discord, provisions isolated sandboxes,
and reports results back to the channel.

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

To run Foreman, see the [deployment guide](docs/deploy.md).

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
