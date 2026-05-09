# Phase 7 — Finish (TOFU SSH, trace, dbg, snap, daemon, tun -R stub)

**Date:** 2026-05-09
**Branch:** `phase-7/finish`

---

## What landed

Closing out the deferred items from MASTER.md §10. Each is contained and
delivers usable v0 functionality without requiring further user input.

### TOFU SSH host-key verification (`internal/sshlife/ssh.go`)

`Dial()` now defaults to `TOFUHostKey(~/.xpc/known_hosts)`:

* First contact: append `<host> <key-type> <base64-key>` to the file.
* Subsequent contacts: byte-match. Mismatch → refuse with a "host key
  changed (potential MITM)" error.

Tests: `internal/sshlife/tofu_test.go` covers first-contact write,
second-contact match, key-change rejection, and multi-host coexistence.

### `xpc trace start | stop | pull` (`internal/cli/trace.go`)

Wraps Sysinternals procmon.exe.

* `xpc trace start [--binary] [--output] [--runtime]` -- detached spawn
  with `/accepteula /quiet /minimized /backingfile <output>`. Optional
  `/runtime <seconds>` for self-terminating captures.
* `xpc trace stop [--binary]` -- `procmon.exe /Terminate` via a no-shell
  python subprocess (skips cmd.exe's argv quirks).
* `xpc trace pull <vm-path> <host-path>` -- alias for `xpc cp` for the
  .pml file.

### `xpc dbg run | analyze` (`internal/cli/dbg.go`)

One-shot cdb wrappers; persistent debugger sessions are intentionally out
of scope for v0 (tracked under "deferred" with a pointer to `xpc tun` +
`xpc ida start` as the long-running-session path).

* `xpc dbg run <target> [--command] [--binary]` -- launch `<target>`
  (executable or `.dmp` path; auto-uses `-z` for dumps), run `--command`
  followed by `;q`, capture output.
* `xpc dbg analyze <vm:dump>` -- shorthand for
  `xpc dbg run --command '!analyze -v' <dump>`.

### `xpc snap list | create | restore | delete` (`internal/cli/snap.go`)

Talks to the Proxmox PVE HTTP API at `https://<proxmox_host>:8006/api2/json/`
using API token auth (`PVEAPIToken=<user@realm!name>=<secret>`).

Configuration via flags (`--proxmox-host`, `--proxmox-user`,
`--proxmox-token`, `--proxmox-node`, `--proxmox-vmid`,
`--proxmox-insecure`) or env vars (`XPC_PROXMOX_*`). Profile fields
`proxmox_host` and `proxmox_user` are honored as defaults; the secret is
expected via env or flag (we don't extend `~/.xpc/credentials` to hold it
in v0).

Live verification waits until you have a Proxmox node + token to point at.

### `xpc daemon start | stop | status | exec` (`internal/cli/daemon.go`)

A long-lived host-side process that holds warm TLS+session connections per
profile. IPC over `~/.xpc/run/daemon.sock`; one-line JSON requests, one-
line JSON responses (with `stdout_b64` / `stderr_b64` chunks).

Smoke verified end-to-end:
```text
$ ./bin/xpc daemon start &
$ ./bin/xpc daemon status
daemon: pid 64627, socket /Users/nficano/.xpc/run/daemon.sock

$ ./bin/xpc daemon exec -- ver
Microsoft Windows XP [Version 5.1.2600]

$ ./bin/xpc daemon stop
sent SIGTERM to pid 64627
$ ./bin/xpc daemon status
daemon: not running
```

The CLI doesn't auto-route through the daemon yet; that's a follow-up once
the protocol is stable across more workloads.

### `xpc tun -R` (stub)

`-R reverse-spec` returns a clear "not yet implemented" error pointing at
TASKS.md. Real reverse forwarding needs an agent->host `tool.invoke`
primitive; meaningful enough to be its own phase later.

## Tests

* Go unit tests: green (TOFU adds 4 cases; existing 47 unchanged).
* `golangci-lint run`: clean (0 issues).
* Python: 42 passed, 2 skipped corpus indices (unchanged).

## Phase 7 exit gate: PASSED

- [x] TOFU SSH host-key with persistent `~/.xpc/known_hosts` + 4 unit tests.
- [x] `xpc trace start/stop/pull`.
- [x] `xpc dbg run/analyze`.
- [x] `xpc snap list/create/restore/delete` (full Proxmox API path).
- [x] `xpc daemon start/stop/status/exec` (warm-session IPC verified).
- [x] `xpc tun -R` stubbed with a clear error.
- [x] All Go tests + lint green; Python tests green.
- [x] Smoke verified: `xpc daemon exec ver` round-trips through the IPC.
- [x] Session log captured (this file).

## Out of scope for v0

- `xpc dbg attach|run|server` interactive sessions (need persistent
  agent-side tool state; `xpc dbg run` covers the one-shot use case).
- `xpc tun -R` reverse forwarding (needs agent->host tool.invoke).
- Persistent Proxmox token storage in `~/.xpc/credentials` (env var
  `XPC_PROXMOX_TOKEN` is the v0 path).
- Auto-routing of `xpc exec` etc. through `xpc daemon` when one is
  running (the daemon is opt-in for now).
