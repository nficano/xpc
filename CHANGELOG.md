# Changelog

All notable changes to `xpc` will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Phase 6c — `xpc tun` (bidirectional ARCP streams)**:
  - Agent: new `tun.connect` tool opens a VM-side TCP socket and pumps
    bytes to the host via `stream.chunk(delta_b64)` envelopes.
  - Agent: `Connection._dispatch` now also routes client-sourced
    `stream.chunk` and `stream.close` envelopes -- looking up the running
    job by `job_id` and writing/half-closing its `tun_socket`. Non-tun jobs
    drop the chunks silently.
  - Host: `xpc tun -L localPort:vmHost:vmPort` cobra command with a
    reader goroutine + forwarder goroutine + write mutex. The forwarder
    waits on a `jobReady` channel so its first `stream.chunk` doesn't
    race ahead of `job.accepted`.
  - Real-VM verified: xpctl ping JSON probe round-trips through
    `xpc tun -L 19578:127.0.0.1:9578` to the xpctl agent on the VM.
  - First xpc command to exercise both directions of ARCP streaming on a
    single job; foundation for future `xpc ghidra/ida` (RE-server tunnels).

- **Phase 5b — SSH-driven bootstrap + agent lifecycle**:
  - `internal/sshlife` Go package wrapping `golang.org/x/crypto/ssh`:
    password-auth Dial, Run (with timeout + exit-status capture), PutFile /
    PutBytes via the Cygwin `cat > <path>` pattern. Win32 paths auto-
    translated to `/cygdrive/c/...`.
  - `agent/embed.go` ships `agent.py`, `arcp.py`, and a `ManagePy` constant
    (kill / start / restart helper with `os.dup2`-to-NUL detachment) inside
    the Go binary via `//go:embed`.
  - `xpc bootstrap` now: generates RSA-2048 cert + PSK locally, SSHes to
    the VM, uploads all six files, restarts the agent via `manage.py`,
    polls until the listener is up, saves fingerprint + PSK into the
    profile. `--no-deploy` retains the manual-steps mode.
  - `xpc agent {start,stop,restart,tail}` drive `manage.py` over SSH.
    `start`/`restart` wait for the TCP listener before returning so chained
    calls (`agent start; agent ping`) don't race.
  - Real-VM verification at `docs/sessions/phase-5b-ssh-bootstrap.md`.

- **Phase 6 (second wave) — RE-flavored subcommands**:
  - `xpc fetch <url> [vm:path]`: host downloads URL, then `cp` to VM
    (default `C:\xpc\downloads\<basename>`).
  - `xpc edit <vm:path>` [--editor]: cp pull → $EDITOR → cp push if changed.
  - `xpc boot reboot | shutdown` (`shutdown.exe /r|/s /f /t 0`); `pause` /
    `resume` stub UsageError pointing at the Proxmox open question.
  - `xpc send keys -- <text> [--title] [--delay-ms]`,
    `xpc send click [--x --y --button --double]`,
    `xpc send move --x --y` — SendInput-style synthetic input via ctypes.
  - `xpc inj <pid> <vm:dll>` — OpenProcess + VirtualAllocEx +
    WriteProcessMemory + CreateRemoteThread(LoadLibraryA).
  - `xpc dump <pid> [-o] [--full]` — MiniDumpWriteDump via dbghelp.dll
    (Normal or WithFullMemory), bytes streamed back base64. Real-VM:
    22.8 KB minidump of the running xpc agent recovered as a valid
    "Mini DuMP crash report" file.

- **Phase 6 (first wave) — subcommand surface**:
  - Diagnostics: `xpc info`, `xpc net [ipconfig|netstat|route]`, `xpc ps [--filter]`.
  - Registry: `xpc reg {get,set,delete,export}` routed through python-subprocess
    argv to bypass cmd.exe quoting bugs (paths with spaces / backslashes).
  - Services: `xpc svc {list,start,stop,status}` with already-running /
    already-stopped idempotency.
  - Environment: `xpc env list`, `xpc env set` (`setx`).
  - Batch + events: `xpc bat run`, `xpc evt query` (eventquery.vbs).
  - Loop: `xpc watch -- <cmd>` (xpctl-style).
  - Python on the VM: `xpc py {run,local,pip}`.
  - Files: `xpc cp <src> <dst>` (host:/vm: bidirectional, inline base64,
    ~30 MB cap before chunked transfer); `xpc cat`, `xpc head -n`,
    `xpc tail -n`, `xpc find [--glob] [--regex]`, `xpc sum [--algo]`.
  - Reverse-engineering: `xpc dll list <pid>`, `xpc dll regsvr32`,
    `xpc shot [-o]` (BitBlt + GetDIBits → 24-bit BMP, base64-transferred).
  - All commands live in `internal/cli` and reuse `internal/cli/session.go` +
    `internal/cli/run.go` for the standard exec round-trip.
  - Real-VM verification at `docs/sessions/phase-6-subcommands.md`.

- **Phase 5 host CLI** (cobra-based dispatcher):
  - `cmd/xpc/main.go` is the canonical entry point; `internal/cli` houses the
    cobra command tree.
  - `internal/profile` AWS-style split: `~/.xpc/config` (non-secret),
    `~/.xpc/credentials` (PSK + SSH password, base64 PSK), `~/.xpc/state`
    (active profile pointer); 0700 dir, 0600 files; env-var overrides.
  - `xpc configure`, `xpc profile {list,add,remove,use}`, `xpc use <name>`,
    `xpc completion {bash,zsh,fish,powershell}`, `xpc migrate-from-xpctl`,
    `xpc bootstrap` (generates trust material + manual deploy instructions),
    `xpc agent {ping,info}`, `xpc exec` with streaming.
  - Session helper (`internal/cli/session.go`) wraps TLS dial + session.open
    + tool.invoke + stream.chunk → stdout/stderr → terminal envelope reading.
  - Sentinel error types map to exit codes: UsageError → 2,
    ConnectionError → 3, AuthError → 4, RemoteError → propagated.
  - Real-VM verification: `xpc exec ver`, `xpc exec 'dir C:\Python34'`,
    `xpc agent ping`, `xpc agent info`, plus shell completion. Session log
    at `docs/sessions/phase-5-cli.md`.

- Phase 0 investigation document (`docs/INVESTIGATION.md`) capturing xpctl's
  architecture, the live target VM environment, and a complete xpctl-to-xpc
  command-surface mapping.
- Phase 1 architecture decisions (`docs/ARCHITECTURE.md`) — twelve locked
  decisions with rationale, rejected alternatives, risks, and a locked
  subcommand surface.
- ARCP RFC 0001 frozen snapshot (`docs/protocol/RFC-0001.md`).
- Master development prompt (`MASTER.md`) committed at repo root.
- Granular task tracker (`TASKS.md`).
- Repository scaffolding: Go module, CI workflows (lint, test, manual real-VM),
  pre-commit hooks, MIT license, branch protection on `main`.
- **Phase 4 agent core** (`agent/agent.py`):
  - TLS 1.2 server with HMAC-SHA256 envelope verification.
  - Per-connection threaded read loop with concurrent job workers and a
    write lock for serialized outbound envelopes.
  - Tool registry with `exec` (streaming subprocess via per-stream chunk
    pumps + terminal `tool.result`) and `agent.info`.
  - `cancel` envelope kills in-flight subprocesses; `ToolError` surfaces
    as structured `tool.error` + `job.failed`.
  - HKLM Run-key `install-startup` / `remove-startup` / `startup-status`
    sub-commands.
  - Rotating file logger at `C:\xpc\agent.log`.
  - In-process tests via `socketpair` exercising session.open, ping,
    auth failure, dispatch, and ToolError wrapping.
  - `cmd/xpc-exec` Go end-to-end client.
  - Real-VM verification: `ver`, `echo`, `dir C:\Python34`, and an
    `os.listdir(r"C:\\")` python-shell run all stream correctly. Session
    log at `docs/sessions/phase-4-agent.md`.

- **Phase 3 wire protocol foundation** (`docs/PROTOCOL.md`):
  - `internal/arcp` Go package: typed envelope, sorted-key canonical JSON
    marshaling, HMAC-SHA256 sign/verify, length-prefixed framing (4-byte
    big-endian length, max 50 MiB), Crockford-base32 IDs, RFC 3339
    timestamps, full v0 message-type constant table.
  - `internal/transport` Go package: TLS 1.2 dial with self-signed cert
    fingerprint pinning via `VerifyConnection`; RSA cipher suites
    (`ECDHE-RSA-AES{128,256}-GCM-SHA{256,384}`).
  - `agent/arcp.py` Python 3.4-compatible mirror; produces byte-identical
    canonical/sig/framed bytes given the same envelope.
  - Shared corpus `tests/protocol_corpus.json` (generated by
    `cmd/gen-corpus`) with 6 golden envelopes covering ping, session.open,
    tool.invoke, stream.chunk, tool.error, and HTML-special-chars cases.
  - `agent/scripts/echo_server.py` reference TLS+HMAC echo server.
  - `cmd/xpc-roundtrip` Go client for the Phase 3 exit-gate verification.
  - Real-VM round-trip verified against the live XP VM; session log at
    `docs/sessions/phase-3-roundtrip.md`. R1/R8 (TLS-on-Python-3.4) risk
    closed.
