# Contributing

`xpc` is following the phased plan in [MASTER.md](MASTER.md). Before opening a
PR, read:

- [MASTER.md](MASTER.md) — project plan and universal rules
- [TASKS.md](TASKS.md) — current phase, granular checkboxes, who owns what
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — locked architecture decisions

## Universal rules (excerpts from MASTER.md §11)

- **TDD.** Tests are written before implementation.
- **Real-VM verification at every phase gate.** Mock-only verification is not
  acceptable for phase exits.
- **`TASKS.md` is updated in the same commit as the work it describes.**
- **No silent guesses.** When you hit an unknown, add the question to
  `TASKS.md` under "Open questions for user".
- **Every subcommand supports `--output json`** from day one.
- **Every state-changing subcommand has `--dry-run`.**
- **No secrets in logs, ever.**

## Local development

Requirements:

- Go 1.22+
- Python 3.4-compatible style for `agent/` (no walrus operator, no f-strings,
  no `type` hints in function signatures, etc. — match the runtime on the VM)
- `golangci-lint`
- `pre-commit`
- A reachable XP VM with a configured `~/.xpc/{config,credentials}` for
  real-VM tests

```bash
pre-commit install
make build
make test
make lint
```

## Code review

- Phases 0–5 are sequential. Sub-agent dispatch (parallel work) only begins
  after Phase 5 is complete and the protocol + daemon are stable
  (`MASTER.md` §6).
- Each subcommand merge requires a real-VM session log committed under
  `docs/sessions/`.

## Reporting issues

- For bugs: open an issue with reproduction, expected vs actual, environment
  details (host OS, Go version, VM Windows build).
- For security: see [SECURITY.md](SECURITY.md).
