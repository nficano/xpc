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
- [x] `go build ./...` and `go test ./...` pass locally
- [x] First commit on `main` (`3a47815 Initial commit`)
- [x] `gh repo create nficano/xpc --private --source=. --remote=origin --push`
- [?] Configure branch protection on `main` (require PRs, require CI green, no force-push) — verify via `gh api repos/nficano/xpc/branches/main/protection`
- [x] Verify CI run starts and passes on the scaffold (PRs #1–#5 merged green)
- [x] **Phase 2 exit gate:** Repo exists, CI green, push complete. Branch-protection state unverified from CLI.

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

### Deferred to Phase 5 — landed

- [x] Reboot survival via Run-key — verified by hand at `xpc bootstrap` time during Phase 5b.
- [x] `internal/sshlife/` Go package — landed in Phase 5b (`Dial`, `Run`, `PutFile`, `PutBytes`, `TOFUHostKey`).

---

## Phase 5 — Host CLI Core (`xpc`) + `exec` end-to-end

Branch: `phase-5/host-cli`. PR + merge at phase end.

### Cobra command tree

- [x] `github.com/spf13/cobra` + `gopkg.in/ini.v1` added to go.mod.
- [x] `internal/cli/root.go` — root cobra command with global flags + lazy `Globals.ResolveProfile`.
- [x] `internal/cli/version.go` — `xpc version`.
- [x] `internal/cli/configure.go` — interactive prompt-driven profile setup.
- [x] `internal/cli/profile.go` — `xpc profile {list,add,remove,use}` plus `--psk-hex` / `--psk-file` import flags.
- [x] `internal/cli/migrate.go` — `xpc migrate-from-xpctl` reads `~/.xpcli/config` → writes `~/.xpc/{config,credentials}`.
- [x] `internal/cli/bootstrap.go` — generates fresh RSA-2048 cert + 32-byte PSK at `~/.xpc/material/<profile>/` and prints the manual deploy steps. SSH-driven end-to-end deploy is Phase 5b.
- [x] `internal/cli/agent.go` — `xpc agent ping` (TLS round-trip latency) and `xpc agent info` (calls the `agent.info` tool). Lifecycle subcommands (`start`/`stop`/`redeploy`) are Phase 5b.
- [x] `internal/cli/exec.go` — `xpc exec` with streaming via `internal/cli/session.go`.
- [x] `internal/cli/completion.go` — bash/zsh/fish/powershell.
- [x] `internal/cli/use.go` — `xpc use <name>` alias.

### Profile system (`internal/profile`)

- [x] Schema: `~/.xpc/config` (INI), `~/.xpc/credentials` (INI), `~/.xpc/state` (single line).
- [x] Loader merging file → env vars (`XPC_*`); CLI flags apply via `Globals.ResolveProfile`.
- [x] Saver writes 0700 dir, 0600 files (verified by `TestSaveAndLoadRoundTrip` perm checks).
- [x] Tests for round-trip, missing fields, env-var precedence (5 tests, all green).

### Exit codes

- [x] 0 ok, 1 generic, 2 UsageError, 3 ConnectionError, 4 AuthError, 5 RemoteError (or remote exit code).
- [x] Verified manually: missing host → 2, remote `cmd.exe /c false` → 1 (remote rc=1).

### Real-VM verification (Phase 5 exit gate)

- [x] `xpc profile add lab --host xp-truvoice-w02 --port 9579 --fingerprint <FP> --psk-file <psk>`.
- [x] `xpc use lab`.
- [x] `xpc agent ping` → pong in 4.4 ms.
- [x] `xpc agent info` → xpc v0.1.0, python 3.4.10, pid + uptime.
- [x] `xpc exec ver` → `Microsoft Windows XP [Version 5.1.2600]`.
- [x] `xpc exec 'dir C:\Python34'` streams full directory listing.
- [x] `xpc exec --shell python` round-trips python source.
- [x] `xpc completion bash` and `xpc completion zsh` produce valid scripts.
- [x] `xpc migrate-from-xpctl` produces correct ~/.xpc/ entries from a synthetic ~/.xpcli/config.
- [x] Session log at `docs/sessions/phase-5-cli.md`.

### Phase 5 exit gate — PASSED

- [x] All Go tests green.
- [x] Lint clean (golangci-lint v2.12.2: 0 issues).
- [x] Real-VM end-to-end `xpc exec` round-trip works.
- [x] Bash + zsh completion verified.
- [x] `TASKS.md` and `CHANGELOG.md` updated.
- [x] Local commit (PR + merge deferred until 1Password unlocked).

### Phase 5b — landed

- [x] `internal/sshlife` Go SSH client (Dial + Run + PutFile/PutBytes) using `golang.org/x/crypto/ssh`.
- [x] `agent/embed.go` ships `agent.py` + `arcp.py` + `ManagePy` inside the Go binary via `//go:embed`.
- [x] `xpc bootstrap` end-to-end: SSH deploy, restart, listener-wait, profile auto-pin.
- [x] `xpc agent start | stop | restart | tail` drive `manage.py` over SSH; `start`/`restart` block on the listener so chained calls work.

### Phase 5b follow-ups

- [x] `xpc daemon` host-side multiplex — landed in Phase 7 (`internal/cli/daemon.go`).
- [x] TOFU SSH host-key verification — landed in Phase 7 (`internal/sshlife/ssh.go` `TOFUHostKey`, `~/.xpc/known_hosts`).
- [x] Cobra arg-validation errors → exit 2 — `UsageError` + `mapExitCode` in `internal/cli/root.go`.
- [x] `internal/output` formatters package — `Encode`, `EncodeRows`, `EncodeKV`, `ParseMode`. `xpc ps`, `xpc agent info`, and `xpc reg get` migrated.

---

## Phase 5b (optional) — `xpc daemon` host-side multiplex — LANDED (Phase 7)

Implementation lives in `internal/cli/daemon.go` rather than a dedicated `internal/daemon/` package.

- [x] Unix socket listener at `~/.xpc/run/daemon.sock`
- [x] CLI auto-detects daemon, falls back to direct
- [x] Connection multiplexing per profile
- [x] `xpc daemon start|stop|status`
- [x] Phase 5b exit: documented in `docs/sessions/phase-7-finish.md`.

---

## Phase 6+ — Subcommand Implementation Loop

Each subcommand: branch `subcommand/<name>`, write spec + tests + impl + real-VM session log + PR.

### 6.1 `xpc cp` — bidirectional file copy
- [x] `host:`/`vm:`/`remote:` prefix parsing + drive-letter heuristic.
- [x] Inline base64 transfer through python-shell subprocess (atomic .tmp+rename on write).
- [x] Real-VM: round-tripped a small text file (host → VM → host) with checksum-equivalent content.
- [x] Chunked transfer for files > 30 MB — 8 MB chunks, SHA-256-verified end-to-end, atomic `.xpc.tmp` + rename. Both upload and download.

### 6.2 `xpc reg get|set|delete|export`
- [x] All four commands route through python-subprocess argv to bypass cmd.exe quoting; works for paths with spaces (e.g. `Windows NT`).
- [x] Real-VM: read `ProductName` and `CSDVersion` from `HKLM\Software\Microsoft\Windows NT\CurrentVersion`.
- [x] `xpc reg get --output json|table` parses `reg query` output into structured `{key,name,type,data}` rows.

### 6.3 `xpc info` / `xpc net`
- [x] `xpc info` runs `systeminfo`.
- [x] `xpc net` combines ipconfig /all + netstat -ano + route print; subcommands `xpc net {ipconfig,netstat,route}` for selective views.
- [x] Real-VM: live output verified.

### 6.4 `xpc ps` / `xpc svc`
- [x] `ps`: structured CSV parse of `tasklist /v /fo csv`; `--filter`; `--output json` honored.
- [x] `svc list | start | stop | status`; idempotent already-running/stopped detection.
- [x] Real-VM: filtered ps shows xpc agent + xpctl agent processes.
- [x] `svc install/uninstall` via `sc create`/`sc delete` — supports `--display-name`, `--start`, `--account`, `--password`, `--depends`; uninstall stops first by default.

### 6.5 `xpc evt`
- [x] `evt query [--log] [--max] [--type]` wraps `eventquery.vbs` (XP-specific).
- [x] `evt tail [--interval]` polls eventquery.vbs and dedupes records so only fresh entries print after the first poll.

### 6.6 `xpc shot` / `xpc send`
- [x] `shot`: BitBlt + GetDIBits ctypes capture, 24-bit BMP, base64 transfer back to local file. Real-VM: 1280×960 BMP captured.
- [x] `send keys -- <text> [--title] [--delay-ms]` — VkKeyScanW + keybd_event sequence.
- [x] `send click [--x --y --button --double]` — SetCursorPos + mouse_event.
- [x] `send move --x --y` — SetCursorPos.

### 6.7 `xpc bat`
- [x] `bat run <vm:path>` invokes a .bat already on the VM with cmd.exe.
- [ ] `bat push-run` (cp + run combo) — `xpc cp` + `xpc bat run` covers this manually for v0.

### 6.8 `xpc tun -L|-R`
- [x] Agent-side `tun.connect` tool + dispatch routing for client-sourced `stream.chunk` / `stream.close` to the job's VM socket.
- [x] Host-side `xpc tun -L localPort:vmHost:vmPort` with reader/forwarder/cancel goroutines and a write mutex.
- [x] Real-VM: forwarded `127.0.0.1:19578 -> 127.0.0.1:9578` and round-tripped xpctl's length-prefixed-JSON ping; agent log shows `tun.connect [job=...] -> 127.0.0.1:9578`.
- [x] `xpc tun -R vmPort:hostHost:hostPort` reverse forward via new `tun.reverse` agent tool. Per-accepted-conn `stream.open channel="reverse_up"` from agent paired with `stream.open channel="reverse_down"` from host on the same `conn_id`. Agent dispatch loop now also handles inbound `stream.open` for downstream registration.

### 6.9 `xpc py`
- [x] `py run`, `py pip`, `py local` (run local file with client injected).
- [x] `py repl` — persistent interactive Python REPL via new `py.repl` agent tool. Bidirectional ARCP streams; line-buffered (Ctrl-D / `exit()` ends the session).
- [ ] Real-VM: REPL session survives multiple commands; pip installs a tiny package.

### 6.10 `xpc dll` / `xpc dump` / `xpc inj`
- [x] `dll list <pid>` — `tasklist /m` wrapper.
- [x] `dll regsvr32 <vm:dll> [--unregister]`.
- [x] `dump <pid> [-o <path>] [--full]` — MiniDumpWriteDump via dbghelp; base64-transferred to host. Real-VM verified (22.8 KB normal-mode dump of xpc agent).
- [x] `inj <pid> <vm:dll>` — OpenProcess + VirtualAllocEx + WriteProcessMemory + CreateRemoteThread(LoadLibraryA). Dry-run printed; live injection deferred until a benign target DLL exists on the VM.

### 6.11 `xpc boot` / `xpc snap`
- [x] `boot reboot` and `boot shutdown` — `shutdown.exe /r/s /f /t 0` via cmd shell. Dry-run verified.
- [x] `boot pause` / `boot resume` — stubs that return a UsageError pointing at TASKS.md open questions; full Proxmox-driven impl still needs host + auth.
- [x] `snap list|create|restore|delete` — Proxmox API integration landed in `internal/cli/snap.go`.
- [ ] Real-VM: take + list + restore a snapshot once Proxmox host + auth land.

### 6.12 `xpc dbg`
- [x] `dbg run` (CDB one-shot) + `dbg analyze` (`!analyze -v` against a minidump). `attach` / `server` modes deferred (live debugging works via `xpc tun -L` to dbgsrv).
- [ ] Real-VM: run cdb against a target, capture output; `analyze` against a minidump produced by `xpc dump`.

### 6.13 `xpc trace`
- [x] `trace start|stop|pull` — procmon wrapper in `internal/cli/trace.go`.
- [ ] Real-VM: trace a tiny program, pull the result, verify entries.

### 6.14 `xpc ghidra` / `xpc ida`
- [x] `xpc ghidra start [--binary] [--port] [--repo]` / `xpc ghidra stop`. Detached spawn via `os.dup2`-to-NUL + `DETACHED_PROCESS`; PID saved to `C:\xpc\ghidra.runlog.pid`. Stop matches `java.exe` with `%ghidra%` in cmdline.
- [x] `xpc ida start [--binary] [--port]` / `xpc ida stop`. Same lifecycle pattern; defaults target `C:\IDA\dbgsrv\win32_remote.exe` on port 23946. Stop matches both `win32_remote.exe` and `dbgsrv.exe`.
- [x] Tunnel decoupled: users run `xpc tun -L <port>:127.0.0.1:<port>` separately.
- [ ] Live verification waits until Ghidra / IDA are installed on the VM.

### Filesystem extras (preserved from xpctl, renamed)

- [x] `xpc cat <vm:path>` — python-shell-driven (backslash-safe).
- [x] `xpc head -n N <vm:path>`.
- [x] `xpc tail -n N <vm:path>` (`-f` follow deferred).
- [x] `xpc find <vm:path> [--glob] [--regex]`.
- [x] `xpc sum <vm:path> [--algo md5|sha1|sha256]`.
- [x] `xpc fetch <url> [vm:path]` — download URL on host, then `cp` to VM (default `C:\xpc\downloads\<basename>`).
- [x] `xpc edit <vm:path>` — pull → $EDITOR → push if changed; `--editor` overrides $EDITOR.
- [x] `xpc watch -- <cmd>` — repeat at `--interval` (default 2s).

### Argv[0] shims (last)

- [x] Dispatcher reads `os.Args[0]`; recognized basenames (`xpcexec`, `xpcreg`, `xpcps`, ...) prepend the matching subcommand. Symlink to wire up: `ln -s xpc xpcexec`. Map lives in `internal/cli/shim.go`.

---

## Current focus

Phases 0–7 plus the post-Phase-7 backlog cleanup are landed. Remaining
open work all requires live access I don't have from the agent:

1. **Real-VM session logs** — exercise the new code against the live VM
   and capture `docs/sessions/*.md`:
   - `xpc py repl`: pip-install a small package; multi-statement session.
   - `xpc cp` chunked: round-trip a >30 MB file; verify checksum.
   - `xpc tun -R`: reverse-forward something to a host service.
   - `xpc dbg`: cdb against a target; `analyze` against a minidump.
   - `xpc trace`: procmon on a small program.
2. **`xpc snap`** real-VM verification — blocked on Proxmox host + auth
   (Open question, line ~395).
3. **`xpc ghidra` / `xpc ida`** live verification — blocked on those
   tools being installed on the VM.
4. **Branch protection on `main`** — verify with
   `gh api repos/nficano/xpc/branches/main/protection`.
5. **(Optional)** uniform `--output json|table` across all subcommands
   that emit structured data (the helpers exist in `internal/output`;
   current adopters are `ps`, `agent info`, and `reg get`).

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
