<p align="center">
  <strong>xpc</strong>
</p>

<p align="center">
  <em>Modern Sysinternals-style remote management toolkit for Windows XP virtual machines.</em>
</p>

<p align="center">
  <em>Status: scaffolding. No production code yet — see <a href="TASKS.md">TASKS.md</a> for current phase.</em>
</p>

---

`xpc` is a single-dispatcher CLI (modeled on AWS CLI v2 and `kubectl`) plus a
Python on-VM agent, communicating over a custom transport that follows the
[ARCP RFC](docs/protocol/RFC-0001.md) envelope contract. It is the modern
successor to [xpctl](https://github.com/nficano/xpctl).

## Quick links

- [MASTER.md](MASTER.md) — master development prompt directing this project
- [TASKS.md](TASKS.md) — single source of truth for project status
- [docs/INVESTIGATION.md](docs/INVESTIGATION.md) — Phase 0 findings
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — Phase 1 architecture decisions
- [docs/protocol/RFC-0001.md](docs/protocol/RFC-0001.md) — ARCP RFC frozen at sign-off
- [docs/protocol/RFC-0002-otel.md](docs/protocol/RFC-0002-otel.md) — OpenTelemetry export design (draft)

## Layout

```
cmd/xpc/        # host CLI binary (Go)
internal/       # Go internals (arcp, transport, profile, cli, output, sshlife)
agent/          # on-VM agent (Python 3.4 compatible) + helper scripts
docs/           # design docs and RFC snapshot
tests/          # integration + real-VM test fixtures
```

## Install

Requires Go 1.25+.

```bash
go install github.com/nficano/xpc/cmd/xpc@latest
```

This drops the `xpc` binary into `$(go env GOBIN)` (or `$(go env GOPATH)/bin`).
Pin a specific release with `@vX.Y.Z` instead of `@latest`.

## Development (placeholder)

```bash
make build       # build the xpc binary
make test        # unit + integration tests
make lint        # go vet + golangci-lint
make fmt         # gofmt
make tidy        # go mod tidy
```

`make test-real-vm` runs the VM-targeted suite once Phase 4+ lands. Until
then, real-VM verification is manual.

## License

[MIT](LICENSE).
