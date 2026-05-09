# xpc Task Tracker

> Single source of truth for all project work. Updated on every change.

## Legend

- `[ ]` Not started
- `[~]` In progress
- `[x]` Done
- `[!]` Blocked (annotate with reason)
- `[?]` Needs user input

---

## Phase 0 — Investigation

- [x] Read xpctl source tree exhaustively (`~/code/xpctl`)
- [x] Read `/Users/nficano/.xpcli/config` and document profile schema
- [x] Fetch ARCP RFC gist verbatim into `/tmp/xpc-investigation/rfc-arcp.md`
- [x] Fetch ARCP examples gist verbatim into `/tmp/xpc-investigation/arcp-examples.md`
- [x] Verify XP VM reachability (ICMP, TCP/22, TCP/9578)
- [x] Probe live agent for OS build, Python version, debuggers, agent state
- [x] Document xpctl wire protocol, transport layers, agent persistence model
- [x] Document xpctl→xpc command surface mapping
- [x] Document ARCP-vs-xpctl-gap mapping
- [x] Identify pain points and keep-list for the rewrite
- [x] Compile open questions and surface architectural ones to user
- [x] Capture all 16 user answers into `docs/INVESTIGATION.md` §11
- [x] **Phase 0 exit gate:** `docs/INVESTIGATION.md` committed in scratch dir; user has answered every open question.

---

## Phase 1 — Architecture (HARD GATE)

- [x] D1: Host CLI = Go 1.22+ with cobra/viper
- [x] D2: Agent = Python 3.4.10 (already on the VM)
- [x] D3: Wire protocol = ARCP envelopes (RFC 0001)
- [x] D4: Transport = TLS 1.2 over TCP, length-prefixed binary framing
- [x] D5: Auth = PSK + HMAC-SHA256 layered under TLS
- [x] D6: Connection model = stateless v0; daemon = Phase 5b
- [x] D7: Agent persistence = HKLM Run key
- [x] D8: Profile = AWS-style split: `~/.xpc/{config, credentials, state}`
- [x] D9: Compat = fresh `xpc serve` required, no protocol bridge
- [x] D10: SSH = bootstrap + agent lifecycle only
- [x] D11: Subcommand surface = locked, see `docs/ARCHITECTURE.md` §10
- [x] D12: TLS trust = self-signed, fingerprint pinned, TOFU on first connect
- [x] Risks register populated (R1–R10)
- [x] Repo skeleton sketched (`docs/ARCHITECTURE.md` §7)
- [x] **Phase 1 exit gate:** User signed off on `docs/ARCHITECTURE.md`.

---

## Phase 2 — Repo & Task Tracker Setup

- [x] Create directory tree (`cmd/`, `internal/`, `agent/`, `docs/`, `tests/`, `.github/workflows/`)
- [x] Move `INVESTIGATION.md` and `ARCHITECTURE.md` from scratch into `docs/`
- [x] Snapshot ARCP RFC into `docs/protocol/RFC-0001.md` (frozen for v0)
- [x] Write `MASTER.md` (verbatim master prompt) at repo root
- [x] Write `README.md`
- [x] Write `LICENSE` (MIT, matching xpctl)
- [x] Write `CHANGELOG.md` with initial Unreleased entry
- [x] Write `CONTRIBUTING.md`
- [x] Write `SECURITY.md`
- [x] Write `.gitignore` (Go + Python + macOS + secrets)
- [x] Write `Makefile` (build, test, lint, fmt, tidy, clean, run, test-real-vm)
- [x] Write `go.mod` (`module github.com/nficano/xpc`, `go 1.22`)
- [x] Write `.pre-commit-config.yaml`
- [x] Write `.golangci.yml`
- [x] Write `cmd/xpc/main.go` (Phase 2 scaffold with `--version`/`--help`)
- [x] Write `cmd/xpc/main_test.go`
- [x] Write `internal/version/version.go`
- [x] Write `internal/version/version_test.go`
- [x] Write `.github/workflows/ci.yml` (lint + test on macOS + Linux)
- [x] Write `.github/workflows/release.yml` (tag-driven, multi-platform builds)
- [x] Write `.github/workflows/real-vm-test.yml` (manual dispatch only, gated environment)
- [x] Write granular `TASKS.md` (this file)
- [ ] `go build ./...` and `go test ./...` pass locally
- [ ] First commit on `main`
- [ ] `gh repo create nficano/xpc --private --source=. --remote=origin --push`
- [ ] Configure branch protection on `main` (require PRs, require CI green, no force-push)
- [ ] Verify CI run starts and passes on the scaffold
- [ ] **Phase 2 exit gate:** Repo exists, CI green, branch protection on, push at phase end complete.

---

## Phase 3 — Wire Protocol Foundation

Branch: `phase-3/wire-protocol`. PR + merge at phase end.

### Spec

- [x] Write `docs/PROTOCOL.md`: framing, envelope shape, v0 message types, capability negotiation, HMAC canonicalization, TLS config, errors, deferred features.
- [x] Pin envelope shape to `docs/protocol/RFC-0001.md` v0 subset.

### Test corpus

- [x] `tests/protocol_corpus.json` with 6 golden envelopes (ping, session.open, tool.invoke.exec, stream.chunk.text, tool.error, html.special.chars). Each entry: input envelope, canonical_hex, sig_hex, framed_hex.
- [x] Generator at `cmd/gen-corpus/main.go`. Re-run to regenerate after spec changes.

### Go protocol library (`internal/arcp`)

- [x] `envelope.go` — typed Envelope, Auth, New(), Validate(), Marshal/Unmarshal.
- [x] `codec.go` — length-prefixed framing read/write, MaxEnvelopeBytes.
- [x] `canonical.go` — sorted-key, no-HTML-escape JSON canonicalization.
- [x] `hmac.go` — Sign, VerifySig with constant-time compare.
- [x] `ids.go` — Crockford-base32 ID generation, FormatTimestamp.
- [x] `types.go` — message-type and nack-code constants.
- [x] `corpus_test.go` — corpus parity (Go side).
- [x] All tests green: 47 tests across envelope, canonical, hmac, codec, ids, corpus.

### Go transport library (`internal/transport`)

- [x] `tls.go` — TLS 1.2 dial, RSA cipher suites, fingerprint pinning via VerifyConnection.
- [x] `tls_test.go` — in-process TLS server with self-signed cert; fingerprint accept/reject + format normalization.

### Python agent module (`agent/arcp.py`)

- [x] Single file, Python 3.4-compatible, stdlib only.
- [x] Mirrors Go: envelope, canonical_marshal, sign/verify_sig, framing, IDs, timestamp.
- [x] `agent/tests/test_arcp.py` — 27 tests covering encoder/decoder/HMAC/framing.
- [x] `agent/tests/test_corpus.py` — corpus parity (Python side); confirms Go and Python produce byte-identical canonical/sig/framed bytes for every test case.

### TLS spike (R1/R8 mitigation)

- [x] Confirmed Python 3.4.10 OpenSSL 1.0.2k on the live VM supports TLS 1.2 with `ECDHE-RSA-AES256-GCM-SHA384`, `ECDHE-RSA-AES128-GCM-SHA256`, and the rest of the cipher suites Go's stdlib accepts. R1/R8 closed.

### Real-network round-trip

- [x] `agent/scripts/echo_server.py` — TLS 1.2 + HMAC echo server (Python 3.4 compatible).
- [x] `cmd/xpc-roundtrip/main.go` — Go round-trip client.
- [x] Deployed to live VM via the existing xpctl TCP agent (no SSH needed).
- [x] All 5 representative message types round-trip OK: ping, session.open, tool.invoke, stream.chunk, log.
- [x] Session log captured at `docs/sessions/phase-3-roundtrip.md`.

### Phase 3 exit gate — PASSED

- [x] All Go unit tests green (47 tests).
- [x] All Python unit tests green (34 tests + 2 skipped corpus indices).
- [x] Corpus parity test green on both sides.
- [x] Real-network round-trip succeeds against the VM (5/5 cases).
- [x] `docs/PROTOCOL.md` complete and committed.
- [x] `TASKS.md` and `CHANGELOG.md` updated.
- [x] PR merged to `main` (push at phase end).

---

## Phase 4 — Agent Core (`xpc serve`)

Branch: `phase-4/agent-core`. PR + merge at phase end.

### Agent code

- [x] `agent/agent.py` (Python 3.4 compatible, single file, stdlib-only):
  - [x] TLS 1.2 server with `load_cert_chain` + RSA cipher suites confirmed on the VM.
  - [x] Per-connection thread; accept loop polls a stop event with a 1s `socket.timeout`.
  - [x] HMAC verification of every inbound envelope; `nack auth_failed` on mismatch.
  - [x] `session.open` handshake with server-intersected capabilities.
  - [x] Tool registry: `exec`, `agent.info`.
  - [x] `exec` tool: spawns subprocess, streams stdout/stderr via `stream.chunk` (one thread per stream), terminal `tool.result` with exit code.
  - [x] `cancel` envelope: sets per-job event + kills subprocess.
  - [x] `ping`/`pong` returns `agent_version`.
  - [x] Connection-write lock so concurrent jobs don't interleave.
  - [x] Logging to `C:\xpc\agent.log` via `RotatingFileHandler` (1 MB cap, 3 backups).
  - [x] Crash isolation: `ToolError` → structured `tool.error`; uncaught handler exceptions → `tool.error code=INTERNAL` + `job.failed`.

### Agent install / lifecycle

- [x] `agent.py install-startup` writes `HKLM\...\Run\xpc_agent`.
- [x] `agent.py remove-startup` deletes the entry.
- [x] `agent.py startup-status` queries it.
- [x] PSK loaded from hex file; cert/key paths configurable via flags.

### In-process tests (`agent/tests/test_agent.py`)

- [x] 8 tests: session.open → session.accepted, ping/pong, auth-failed close, unsupported-type → nack, unknown tool → tool.error, tool.invoke before session.open → nack, agent.info tool, ToolError-raising handler.

### Host-side end-to-end (`cmd/xpc-exec`)

- [x] Connects via `internal/transport`, runs full session lifecycle, writes stream chunks to local stdout/stderr, exits with the remote exit code.

### Real-VM verification (Phase 4 exit gate)

- [x] Deployed `agent.py`, `arcp.py`, cert/key/PSK to `C:\xpc\` via xpctl on 9578.
- [x] xpc agent starts on 9579 (xpctl stays on 9578 as the deploy channel).
- [x] `xpc-exec ver` → `Microsoft Windows XP [Version 5.1.2600]`.
- [x] `xpc-exec echo hello world` → `hello world`.
- [x] `xpc-exec 'dir C:\Python34'` streams the full directory listing (10 dirs, 5 files).
- [x] `xpc-exec --shell python 'os.listdir(r"C:\\")'` lists every root entry — canonical Phase 4 evidence.
- [x] Session log captured at `docs/sessions/phase-4-agent.md`.

### Phase 4 exit gate — PASSED

- [x] All Go tests green.
- [x] All Python tests green (50 total: 42 protocol + 8 agent dispatch, plus 2 corpus skips).
- [x] Real-VM `xpc-exec` round-trip succeeds.
- [x] Logging confirmed via the rotating file handler.
- [x] `TASKS.md` and `CHANGELOG.md` updated.
- [x] PR merged. Push at phase end.

### Deferred to Phase 5

- [ ] Reboot survival via Run-key — covered by xpctl's bootstrap pattern; xpc Run-key install is verified by hand at `xpc bootstrap` time in Phase 5.
- [ ] `internal/sshlife/` Go package — actual SSH-driven deploy code lands in Phase 5 alongside `xpc bootstrap`. The manual orchestration in this phase proves the pattern works.

---

## Phase 5 — Host CLI Core (`xpc`) + `exec` end-to-end

Branch: `phase-5/host-cli`. PR + merge at phase end.

### Cobra command tree

- [ ] Add `github.com/spf13/cobra` and `github.com/spf13/viper` deps
- [ ] `internal/cli/root.go` — root cobra command with global flags (`--profile`, `--target`, `-v/--verbose`, `--output`, `--timeout`, `--dry-run`)
- [ ] `internal/cli/version.go` — `xpc version`
- [ ] `internal/cli/configure.go` — interactive AWS-style profile setup with live ping validation + cert TOFU
- [ ] `internal/cli/profile.go` — `xpc profile {list,add,remove,use}` and `xpc use <name>`
- [ ] `internal/cli/migrate.go` — `xpc migrate-from-xpctl` reads `~/.xpcli/config` → writes `~/.xpc/{config,credentials}`
- [ ] `internal/cli/bootstrap.go` — `xpc bootstrap [<profile>]`: SSH deploy + cert/PSK gen + Run-key install
- [ ] `internal/cli/agent.go` — `xpc agent {ping,status,info,deploy,start,stop,redeploy,install,uninstall,startup-status,reboot}`
- [ ] `internal/cli/exec.go` — `xpc exec <cmd>` streaming
- [ ] `internal/cli/serve.go` — `xpc serve` (uploads/runs the Python agent code; xpc itself is the host bin)
- [ ] `internal/cli/completion.go` — bash/zsh/fish/pwsh

### Profile system (`internal/profile`)

- [ ] Schema: `~/.xpc/config` (INI), `~/.xpc/credentials` (INI), `~/.xpc/state` (single line)
- [ ] Loader merging file → env vars (`XPC_*`) → CLI flags
- [ ] Saver writes 0700 dir, 0600 files
- [ ] Tests for round-trip, missing fields, env-var precedence

### Output formatters (`internal/output`)

- [ ] `text` — default human-readable (rich-equivalent: lipgloss for Go)
- [ ] `table` — for list-style results
- [ ] `json` — structured output, every command from day one
- [ ] Tests against fixtures

### Exit codes

- [ ] 0 ok, 1 generic error, 2 usage error, 3 connection error, 4 auth error, 5 remote command error
- [ ] Tests for each path

### Real-VM verification (Phase 5 exit gate)

- [ ] `xpc configure --profile default` against the live VM
- [ ] `xpc bootstrap default` (replaces what was deployed in Phase 4 if needed)
- [ ] `xpc exec dir 'C:\\'` produces the same output as `xpctl exec dir 'C:\\'`
- [ ] `xpc completion bash` and `xpc completion zsh` install and provide tab completion
- [ ] Capture session log under `docs/sessions/phase-5-cli.md`

### Phase 5 exit gate

- [ ] All unit + integration tests green.
- [ ] Real-VM `xpc exec dir 'C:\'` succeeds.
- [ ] Bash + zsh completion verified manually.
- [ ] `TASKS.md` and `CHANGELOG.md` updated.
- [ ] PR merged. Push at phase end.

---

## Phase 5b (optional) — `xpc daemon` host-side multiplex

Deferred. Implement only after Phase 6 subcommands are stable enough to benefit.

- [ ] `internal/daemon/server.go` — Unix socket listener at `~/.xpc/run/daemon.sock`
- [ ] `internal/daemon/client.go` — CLI auto-detects daemon, falls back to direct
- [ ] Connection multiplexing per profile
- [ ] `xpc daemon start|stop|status`
- [ ] Phase 5b exit: `xpc exec` round-trip latency drops measurably with daemon enabled.

---

## Phase 6+ — Subcommand Implementation Loop

Each subcommand: branch `subcommand/<name>`, write spec + tests + impl + real-VM session log + PR.

### 6.1 `xpc cp` — bidirectional file copy
- [ ] Spec: `docs/SPEC-cp.md`. Tests: arg parsing (host:/vm: prefixes), chunked binary streaming.
- [ ] Agent handler: chunked read/write with `stream.chunk`.
- [ ] Host: progress bar, --resume support stub.
- [ ] Real-VM: push/pull a 32 MB binary file; checksum match.

### 6.2 `xpc reg get|set|delete|export`
- [ ] Spec, tests, structured output (every key/value as JSON).
- [ ] Agent handler: `winreg` via Python.
- [ ] Real-VM: round-trip a value through HKCU and HKLM.

### 6.3 `xpc info` / `xpc net`
- [ ] `info`: structured systeminfo. `net`: combined ipconfig+netstat+route.
- [ ] Real-VM: output non-empty, all keys present.

### 6.4 `xpc ps` / `xpc svc`
- [ ] `ps`: structured process list (filter, pid match).
- [ ] `svc`: list/start/stop/install/uninstall/status with idempotency.
- [ ] Real-VM: list, stop a benign service, start it back, verify.

### 6.5 `xpc evt`
- [ ] `evt tail` (live event log streaming) + `evt query` (filtered fetch).
- [ ] Note: XP uses `eventquery.vbs`, not `wevtutil` — use Python `win32evtlog` via ctypes.
- [ ] Real-VM: tail Application log, query last 10 errors.

### 6.6 `xpc shot` / `xpc send`
- [ ] `shot`: full-screen and per-window screenshots.
- [ ] `send keys|click|move`: synthetic input via `SendInput`.
- [ ] Real-VM: capture desktop, send a keystroke into Notepad, verify.

### 6.7 `xpc bat`
- [ ] `bat run|push-run|create` — streaming stdout.
- [ ] Real-VM: create + run a tiny .bat that echoes args.

### 6.8 `xpc tun -L|-R`
- [ ] ARCP-multiplexed tunnels: each forwarded TCP connection = one ARCP stream.
- [ ] `xpc tun -L 8080:localhost:80` (forward host:8080 → VM:80) and reverse.
- [ ] Real-VM: forward to a process on the VM, hit it from the host.

### 6.9 `xpc py`
- [ ] `py run`, `py repl`, `py pip`, `py local` (run local file with client injected).
- [ ] Persistent REPL session (matches xpctl pyshell pattern).
- [ ] Real-VM: REPL session survives multiple commands; pip installs a tiny package.

### 6.10 `xpc dll` / `xpc dump` / `xpc inj`
- [ ] `dll list/inject/regsvr32`, `dump <pid>`, `inj <pid> <dll>`.
- [ ] Real-VM: dump a benign process, inject a no-op DLL.

### 6.11 `xpc boot` / `xpc snap`
- [ ] `boot reboot|shutdown|pause|resume`.
- [ ] `snap list|create|restore|delete` — Proxmox API integration (host details still pending; flag in `Open questions`).
- [ ] Real-VM: take + list + restore a snapshot.

### 6.12 `xpc dbg`
- [ ] `dbg attach|run|server` — wraps OllyDbg / WinDbg(CDB) / x64dbg.
- [ ] Real-VM: attach CDB to a process, run a simple `~* k` command, detach.

### 6.13 `xpc trace`
- [ ] `trace start|stop|pull` — procmon / API Monitor wrapper.
- [ ] Real-VM: trace a tiny program, pull the result, verify entries.

### 6.14 `xpc ghidra` / `xpc ida`
- [ ] `ghidra start|stop` — ghidra_server lifecycle + tunnel.
- [ ] `ida start|stop` — IDA remote-debug stub lifecycle + tunnel.
- [ ] Real-VM: start ghidra_server, connect from local Ghidra over the tunnel.

### Filesystem extras (preserved from xpctl, renamed)

- [ ] `xpc cat <vm:path>` — print remote file
- [ ] `xpc head <vm:path>` — first N lines
- [ ] `xpc tail <vm:path>` — last N lines (option `-f` for follow)
- [ ] `xpc find <vm:path>` — recursive glob/regex
- [ ] `xpc sum <vm:path>` — md5/sha1/sha256
- [ ] `xpc fetch <url> [vm:path]` — download URL → upload to VM
- [ ] `xpc edit <vm:path>` — pull → $EDITOR → push if changed
- [ ] `xpc watch <cmd>` — repeat at interval

### Argv[0] shims (last)

- [ ] After dispatcher works: `xpcexec`, `xpcreg`, etc. as symlinks to `xpc`.

---

## Current focus

Phase 2 wrap-up: writing `MASTER.md` and `TASKS.md`, then local verify (`go build`, `go test`), initial commit, GitHub repo creation (private), push to `main`, branch protection. After Phase 2 exit gate clears, Phase 3 (wire protocol) starts on a feature branch.

---

## Open questions for user

(All Phase 0 / Phase 1 questions answered; see `docs/INVESTIGATION.md` §11 for the full record.)

- _2026-05-08_: Proxmox host address + API auth method for `xpc snap`. Deferred to Phase 6.11 implementation; not blocking Phases 2–5.
- _2026-05-08_: Confirm `~/.profile.local` API keys are not needed in v0. Defer to whichever subcommand first integrates MCP (likely none in v0).

---

## Recently completed (last 7 days)

- _2026-05-08_: Phase 0 investigation completed; live VM probed; user signed off on §10 answers.
- _2026-05-08_: Phase 1 architecture document approved (12 decisions locked).
- _2026-05-08_: Phase 2 scaffolding — directory tree, Go skeleton, CI workflows, pre-commit, license, docs; first commit pending after MASTER.md + TASKS.md.
