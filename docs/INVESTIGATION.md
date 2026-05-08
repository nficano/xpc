# xpc Phase 0 Investigation

> Status: draft — pending user answers to open questions in §10 before Phase 1 (architecture).
>
> Authored by the implementation agent during Phase 0 of `MASTER.md`. Lives in `/tmp/xpc-investigation/` until the `nficano/xpc` repo is created in Phase 2, at which point it moves to `docs/INVESTIGATION.md`.

---

## 1. Mission recap

Build `xpc`, a single-dispatcher Sysinternals-style remote management toolkit for Windows XP VMs, modeled on AWS CLI / kubectl. Replaces the existing working tool `xpctl` with a more elegant, more tested, more extensible architecture. Daily-use target: Nick's TruVoice/SAPI4 Klatt synthesizer reverse-engineering work.

Hard rules (from `MASTER.md`):
- Phased TDD with hard gates between phases.
- Real-VM verification at every gate (no mock-only acceptance).
- `TASKS.md` is single source of truth.
- No silent guesses on architectural unknowns.
- Every subcommand: `--output json`, `--dry-run` for state-changing ops, idempotent where possible.

---

## 2. Live target VM

| Property | Value |
|---|---|
| Hostname | `xp-truvoice-w02.home.nickficano.com` |
| IP | `172.16.20.173` (private, Nick's home LAN) |
| Reachability | ICMP ✓, TCP/22 ✓, TCP/9578 ✓ — round-trip ~5 ms |
| OS | Windows XP SP3, build `5.1.2600`, 32-bit |
| CPU | Intel Skylake-class (`x86 Family 6 Model 94 Stepping 3`) — modern host, virtualized |
| Memory | 2047 MB total, ~1723 MB free |
| Disk C:\\ | 32 GB total, ~10 GB free |
| Python | `C:\Python34\python.exe` (3.4.10, the last version that runs on XP) |
| Cygwin | sshd present on TCP/22 (user `cyg_server`, password `xpctl-sshd` per bootstrap) |
| Debuggers | `olly` `C:\OllyDbg\ollydbg.exe`, `cdb` `C:\Program Files\Debugging Tools for Windows\cdb.exe`, `windbg` (same dir), `x64dbg` `C:\x64dbg\release\x32\x32dbg.exe` — all installed |
| Agent | xpctl agent v0.1.0, PID 1800, uptime ~5.8 days at probe time |
| .NET Framework | **not yet probed** — official ceiling on XP SP3 is .NET 4.0; relevant only if .NET is chosen as agent language in Phase 1 |

VM probe was a raw socket → length-prefixed-JSON `ping` / `agent_info` / `sysinfo` round-trip; no `xpctl` binary required. Confirms the protocol and agent are working today.

---

## 3. xpctl reference implementation

### 3.1 Layout

```
src/xpctl/
  protocol.py            # length-prefixed JSON wire format
  client.py              # high-level XPClient, DebuggerProxy
  config.py              # ~/.xpcli/config (INI), profile loader/saver
  deploy.py              # AgentDeployer: SMB+SSH push, lifecycle, install
  debuggers.py           # debugger metadata
  resources.py           # bundled-asset accessors
  templates/             # Jinja2 templates for bootstrap scripts
  transport/
    base.py              # abstract Transport
    factory.py           # ConnectionProfile, mode dispatch (auto|tcp|ssh)
    tcp.py               # direct TCP to agent.py
    ssh.py               # paramiko -> Cygwin bash on the VM
    ssh_support/         # bat/python/sftp/shell/install/translation helpers
  cli/
    __init__.py          # click root, configure, dispatch register_*
    admin.py             # ping, agent {deploy,start,stop,status,...}, snapshot, setup
    debug.py             # debug {list,ps,olly,windbg,x64dbg}
    exec.py              # exec, bat, reg, script, shell, watch
    files.py             # upload/download/ls/rm/push-run/cat/head/tail/find/checksum/edit/fetch-exe
    reverse.py           # dll, com, mem, gui
    system.py            # sysinfo, ps, net (netstat, portfwd), svc, env
    support.py           # shared helpers, common_options, _client(ctx)
  assets/
    agent.py             # the on-VM Python 3.4 agent (single file, no deps)
    bootstrap_xpctl.bat  # XP first-boot bootstrap
    scripts/             # remote Python scripts shipped to the VM (mem_read, dll_inject, gui_*, etc.)
    installers/          # bundled python/cygwin/ollydbg/x64dbg ZIPs (force-included into wheel)
tests/                   # pytest, 9 test modules, ~640 SLOC
installs/                # raw installer archives (~67 MB)
docs/                    # MkDocs Material site
```

### 3.2 Wire protocol (TCP)

- **Framing:** `[4-byte big-endian uint32 length][UTF-8 JSON payload]`. Max 50 MB.
- **Envelope:** `{id, type, action, params, status, data, error}` — `MessageType` enum has `request|response|stream`, but **`stream` is defined and unused**. All current handlers are unary request/response.
- **Auth:** none — cleartext TCP, relies on the LAN being trusted. Password in profile is for the SSH transport only.
- **Transport-level guarantees:** none beyond TCP. No retry semantics in messages, no idempotency keys, no cancellation, no resume, no streaming, no backpressure, no tracing.
- **Action surface (agent):** `ping, exec, bat_run/create, file_upload[/_start/_chunk/_end], file_download, file_list/delete/stat, sysinfo, proclist, services, agent_info/shutdown, debug_*, install_startup/remove_startup/startup_status, reboot, pyshell_eval/reset`.

The chunked file-upload subprotocol (`file_upload_start` → `file_upload_chunk*` → `file_upload_end` keyed by `transfer_id`) is the only piece that *resembles* a stream. Everything else is one-shot. File **download** is single-blob base64 in JSON — fine for small files, bad for the 32-bit XP `python.exe` heap on large pulls.

### 3.3 Two divergent transports

`TransportFactory` exposes `auto|tcp|ssh`:

- **TCP transport** speaks the JSON protocol above directly to `agent.py` on port 9578. It supports the **full action surface** including debugger sessions, persistent Python REPL, DLL injection, mem read/dump, GUI screenshot/sendkeys.
- **SSH transport** does **not** speak the wire protocol at all. It uses paramiko to invoke Cygwin bash, which in turn shells out to `cmd.exe` or `C:\Python34\python.exe` running an embedded Python script. Structured returns are extracted from stdout via the `__XPSH_JSON__` sentinel marker. Subset of actions: `ping, exec, bat_*, file_*, sysinfo, proclist, services, install_startup, remove_startup, startup_status, reboot, agent_info/shutdown` — debugger and pyshell actions are unsupported.

`auto` mode probes TCP/9578 first and falls back to SSH/22. The SSH path exists primarily so first-boot diagnostics work before the agent is installed, and so file/exec ops survive an agent crash.

This dual-stack is **the largest architectural smell** in xpctl. Going forward, `xpc` should converge on one wire protocol and use SSH only for first-boot bootstrap, not as a peer transport.

### 3.4 Profile / configuration

Path: `~/.xpcli/config`. INI format via `configparser`. Per-profile fields: `hostname, port, transport, username, password`. Directory `0o700`, file `0o600`. Active profile selected via `--profile` flag or env vars `XPCTL_HOST/PORT/TRANSPORT/SSH_USER/SSH_PASSWORD`. `xpctl configure` walks an AWS-style interactive prompt and validates the connection live before saving.

Live config snapshot (`/Users/nficano/.xpcli/config`): one `[default]` profile pointing at `xp-truvoice-w02:9578` with `transport = ssh` (username and password elided). Note the apparent inconsistency: `transport=ssh` with `port=9578` is reconciled at `transport/factory.py:86` — when SSH is selected and port is the agent default 9578, the factory silently rewrites port to 22. That's a footgun.

### 3.5 The on-VM agent (`assets/agent.py`)

- Single Python 3.4 file, no third-party deps; uses `ctypes` for Win32 calls.
- Threaded TCP server: one `ClientHandler` thread per connection, no concurrency limit.
- Persistence: registered in `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Run` under value name `xpctl_agent` (i.e., logs in as the interactive user on boot). **Not a Windows service.**
- Lifecycle: started by the bootstrap batch via `start /B`, manageable later via TCP `agent_shutdown` or WMIC kill via SSH.
- Persistent-state features:
  - Per-session interactive Python console (`code.InteractiveConsole`) keyed by `session_id`. Captures stdout/stderr per push. Used by `xpctl shell`. Important for SAPI4/Klatt iteration.
  - Per-session debugger handles (`Popen` for olly/cdb/windbg/x64dbg) with a background reader thread for piped sessions.
  - In-flight chunked file uploads keyed by `transfer_id`.
- Bootstrap (`bootstrap_xpctl.bat`):
  - Installs Cygwin + sshd from a pinned 2016 mirror (`http://ctm.crouchingtigerhiddenfruitbat.org/pub/cygwin/circa/2016/08/30/104223/`)
  - Installs Python 3.4.10 to `C:\Python34`
  - Drops `agent.py` into `C:\xpctl\agent.py`
  - Opens firewall TCP/22 + TCP/9578
  - Waits up to 30 s for the listener

### 3.6 xpctl ↔ target xpc command surface mapping

| Target `xpc` command | Source in `xpctl` | Status |
|---|---|---|
| `xpc configure` | `xpctl configure` | direct port (rename config dir to `~/.xpc/`) |
| `xpc profile list/add/remove/use` | absent (only `--profile` flag) | new |
| `xpc use <profile>` | absent | new |
| `xpc completion bash/zsh/fish/pwsh` | absent | new (cobra/clap/click can generate) |
| `xpc exec` | `xpctl exec` | port; **must** stream stdout/stderr in xpc |
| `xpc cp` | `xpctl upload` + `xpctl download` | unify into one bidirectional `cp` with `host:`/`vm:` prefixes |
| `xpc reg get/set/delete/export` | `xpctl reg read/write/delete/export` | rename `read→get`, `write→set`; structured output |
| `xpc bat` | `xpctl bat run/push-run/create` | port |
| `xpc py run/repl/pip` | `xpctl shell`, `xpctl script` (no pip) | port + add `pip` |
| `xpc tun -L/-R port:host:port` | `xpctl net portfwd` (only `-L`, shells out to local `ssh`) | rebuild over agent stream multiplexing; add reverse forward |
| `xpc boot reboot/shutdown/pause/resume` | `xpctl agent reboot` only | extend (`shutdown` via Win32, `pause/resume` via Proxmox) |
| `xpc snap list/create/restore/delete` | `xpctl snapshot save/restore` (Proxmox + VBox) | extend with `list`/`delete`; profile-stored Proxmox host |
| `xpc ps` | `xpctl ps` | port |
| `xpc svc list/start/stop/install/uninstall` | `xpctl svc list/start/stop/status` | extend (`install/uninstall` via `sc create`/`sc delete`) |
| `xpc dll <pid>` | `xpctl dll list/inject/regsvr32` | port |
| `xpc dump <pid>` | `xpctl mem dump` | rename, support `--full` (full minidump) |
| `xpc inj <pid> <dll>` | `xpctl dll inject` | rename |
| `xpc shot` | `xpctl gui screenshot` | rename |
| `xpc send keys/click/move` | `xpctl gui sendkeys` (only `keys`) | extend (`click`, `move` via `mouse_event` / `SendInput`) |
| `xpc evt tail/query` | absent | new (`wevtutil` on XP is `eventquery.vbs` — special-case) |
| `xpc info` | `xpctl sysinfo` | rename |
| `xpc net` | `xpctl net netstat` only | extend (combine `ipconfig /all` + `netstat -ano` + `route print`) |
| `xpc dbg attach/run/server` | `xpctl debug olly/windbg/x64dbg launch/attach/cmd/...` | redesign UX; add `dbgsrv` lifecycle |
| `xpc trace start/stop/pull` | absent | new (procmon / API Monitor wrapper) |
| `xpc ghidra start/stop` | absent | new (ghidra_server lifecycle + tunnel) |
| `xpc ida start/stop` | absent | new (IDA remote-debug stub + tunnel) |
| `xpc serve` | `agent.py` (not exposed via CLI) | new — same binary as host CLI per master prompt |
| `xpc daemon` | absent | new — host-side multiplex daemon over Unix socket / named pipe |
| `xpc migrate-from-xpctl` | n/a | new — read `~/.xpcli/config`, write `~/.xpc/{config,credentials}` |

### 3.7 Tests

`tests/` has 9 modules, ~640 SLOC. Coverage focus:
- `test_protocol.py` — wire format encode/decode, length-prefix, max size.
- `test_client.py` — `XPClient` request shape and connection lifecycle.
- `test_transport_tcp.py` — minimal smoke.
- `test_ssh_transport.py` — paramiko paths (mocked).
- `test_deploy.py` — `AgentDeployer` orchestration.
- `test_cli.py` — click invocations end-to-end (mocked transport).
- `test_configure.py` — interactive prompt flow.
- `test_resources.py` — bundled asset accessors.
- `test_bootstrap_bundle.py` — bootstrap directory contents.

Gaps: no real-VM integration suite, no streaming tests (because there's no streaming), no auth tests (because there's no auth), no fuzz/property tests on the wire format.

### 3.8 Top pain points (what `xpc` should fix)

1. **Two divergent transports** with different action surfaces, different code paths, and a footgun port-rewrite. Converge on one.
2. **No auth, no TLS.** Cleartext JSON over the LAN. Trivial to fix with PSK+HMAC or self-signed mTLS; XP SP3 + .NET 4.0 / Schannel only goes to TLS 1.0 by default but TLS 1.2 backports exist.
3. **No streaming.** `exec` blocks until the command exits; large `proclist`/`netstat` block; debugger output is poll-only via `debug_log`.
4. **No cancellation.** Once a request is in flight you can't kill it short of dropping the connection.
5. **No port forwarding through the agent.** `net portfwd` shells out to local `ssh`; this works but means tunnels die when SSH dies and have nothing to do with the daemon.
6. **No persistent host-side daemon.** Every connection is fresh — no connection pooling, no warm session, no command-batch optimization.
7. **No structured tracing or observability.** Useful even on a single-VM workflow because `xpc dbg` + `xpc trace` will produce intermixed event streams.
8. **No service install.** Registry Run key works but ties the agent to interactive login. Installing as a Windows service is more robust.
9. **No durable state across reconnects.** Important for long debugger sessions and large file pulls; ARCP `resume` + checkpoints addresses this.
10. **No structured registry/event-log/service ops.** Currently most things shell out to `cmd.exe /c reg query ...` and parse text. A typed result schema is better.

### 3.9 Top things to keep

1. AWS-style `configure` flow with live validation.
2. Profile file as INI under a `~/.xpc/` dir; split secrets into `~/.xpc/credentials` (AWS-style) — and add a one-shot `xpc migrate-from-xpctl`.
3. Length-prefixed JSON wire framing (compatible with ARCP envelope; see §4).
4. The action-handler dispatch table pattern in `agent.py` — clean and minimal.
5. Persistent Python REPL pattern (`code.InteractiveConsole` per session). Critical for SAPI4 work.
6. Capability detection for debuggers at startup.
7. Idempotent retry with backoff in TCP connect (`transport/tcp.py`).
8. Bundled installer archives in the repo for offline XP bootstrap.
9. The bootstrap-bundle generator (`xpctl setup bootstrap` → portable directory). Replicate as `xpc bootstrap`.
10. Embedded remote scripts pattern (`assets/scripts/*.py`) for actions that are easier as Python on the VM than as wire-protocol primitives. With ARCP, these become "tools" the agent registers.

---

## 4. ARCP gist analysis

Both gists are about **ARCP (Agent Runtime Control Protocol) — RFC 0001**, authored by Nick. ARCP is generic, transport-agnostic, schema-first; explicitly designed to complement (not replace) MCP. The use-cases gist illustrates ARCP via four production-shaped scenarios (support refund, code review, data ingestion, incident response) — all transport-neutral, none XP-specific.

The strong inference: ARCP is intended to be the wire protocol for `xpc`. xpctl's existing protocol (length-prefixed JSON envelopes, request/response) is already ARCP-shaped — adopting ARCP is **closing the gaps** rather than rewriting from scratch. The relevant ARCP capabilities map directly onto xpctl's pain points:

| xpctl gap | ARCP capability that closes it |
|---|---|
| No streaming `exec` output | `stream.open` + `stream.chunk` |
| No cancellation | `cancel` envelope keyed by `job_id` |
| No durable file pulls | `job.checkpoint` + `resume` with `after_message_id` |
| No backpressure on large pulls | `backpressure` envelope |
| No tracing across multi-step workflows (`xpc dbg` → `xpc dump`) | `trace_id`, `span_id`, `parent_span_id`, `causation_id` |
| State-changing ops (reboot, snapshot restore) without confirmation | `permission.request` / `permission.grant` with `lease_id` and `expires_at` |
| `agent.delegate` / `agent.handoff` | unused for single-VM but useful if `xpc` ever talks to multiple VMs through one runtime |
| MCP integration (future: an LLM that drives `xpc` via MCP and receives streaming output via ARCP) | RFC §16 mapping table |

Mandatory ARCP transports per RFC §17: WebSocket, stdio. Recommended: HTTP/2, QUIC. **None of these are "raw TCP with length-prefixed JSON," which is what xpctl currently uses.** This is a real architectural decision: keeping the existing TCP framing means we deviate from ARCP's mandatory transport list (and lose the ability to use generic ARCP tooling later); switching to WebSocket means the agent on Python 3.4 needs a WebSocket library that runs on XP. Stdio is viable for the host-side daemon ↔ host CLI hop but not for host ↔ VM (different machines).

Pre-Phase-1 sketch of options:

- **A.** Adopt ARCP envelope shape, keep length-prefixed-binary framing as a custom transport (with TLS). Pragmatic; deviates from RFC §17.
- **B.** Adopt ARCP fully, transport = WebSocket over TLS. Need WS library that runs on Python 3.4 / .NET 4.0 / a small C++ build.
- **C.** Hybrid: WS for host ↔ daemon (browser-friendly, OTel-friendly), custom binary for daemon ↔ VM agent. Daemon translates between them.

---

## 5. Constraints we already know

- **TLS on XP SP3:** Schannel ceiling without backports is TLS 1.0; TLS 1.2 is available via [KB3140245](https://support.microsoft.com/kb/3140245) for some scenarios but generally not on XP SP3. Python 3.4 ships OpenSSL 1.0.x which supports TLS 1.2 if linked correctly. Practical default: TLS 1.2 from Python 3.4 client/server, accepting that we control both ends.
- **Python 3.4 available on the VM** — the agent can stay in Python with no install pain. This biases toward Python as the agent language unless a strong case for .NET/C++ emerges.
- **gRPC is effectively dead on XP** — the .NET gRPC stack requires modern .NET; Python `grpcio` doesn't ship Python 3.4 wheels.
- **No `service` install today** — adding `sc create` / `pywin32` `ServiceBase` is incremental work.
- **WoW64 / 64-bit issues do not apply** — VM is x86 32-bit; our injection / dump / debugger paths are all single-bitness.
- **The VM is on a private home LAN.** Untrusted-network threat model is real but not urgent. Auth/TLS is "nice to have" rather than "must have for correctness," which gives us freedom in Phase 1.

---

## 6. installs/ inventory

- `python-3.4.10.zip` — 30 MB, unofficial Python build for XP.
- `setup-x86-2.874.exe` / `cygwin-2.874.exe` — Cygwin setup pinned to 2016 snapshot.
- `ollydbg-1.10.zip` — 1.3 MB, OllyDbg.
- `x64dbg-2025.08.19.zip` — 30 MB, last x64dbg snapshot known to work.
- (Mentioned in README but absent: `windbg`, `cdb` — placeholders.)

Already on the VM: all four debuggers installed; Python 3.4 present; Cygwin sshd present.

---

## 7. Repo state

- `/Users/nficano/code/xpc` exists, is a git repo with no commits and no files (only `.git/`).
- `nficano/xpc` does **not** yet exist on GitHub (per master prompt §6, we create it in Phase 2 once architecture is signed off).
- `nficano/xpctl` exists at github.com/nficano/xpctl per `pyproject.toml` URLs.

---

## 8. Risks I'm watching

1. **Bigger scope than it looks.** 25+ subcommands × `--output json` × `--dry-run` × tests × real-VM verification = a lot. Phase 6+ parallelization will help but only after phases 0–5 establish stable infra.
2. **Service install on XP is finicky.** If we go .NET, debugging service startup on XP is painful. If we stay in Python 3.4, `pywin32` for XP is rare and version-pinned. Sticking with the Run-key model in v0 and treating service install as a later upgrade reduces risk.
3. **TLS+auth on Python 3.4:** doable but the default OpenSSL on a manual Python 3.4 install may be old. Need to confirm during Phase 4.
4. **ARCP RFC is still Draft.** If RFC details shift during construction, we'll be re-implementing message shapes. Lock the spec we build against to a frozen version (e.g. tag the RFC at a commit).
5. **Bootstrap bundle drift.** The 2016 Cygwin mirror is a single point of failure for first-boot. Mitigation: keep the bundled `cygwin-2.874.exe` and ship it with `xpc bootstrap`.

---

## 9. Things I have decided I'm confident about (informational, not for sign-off)

These don't need user input but I'm flagging them so I don't spring them later.

- New config dir: `~/.xpc/` with `config` (non-secret) + `credentials` (secret), AWS-style split. Migrator reads `~/.xpcli/config`.
- Default port for the agent stays `9578` to match xpctl + already-open firewall.
- The host CLI binary is a single `xpc` with `cobra/clap/click`-style subcommands. Argv[0] shims (`xpcexec`, `xpcreg`) are deferred to after the main dispatcher works.
- The on-VM agent stays a single deployable artifact; persistence model (Run key vs service) is a Phase-1 decision.
- The host-side daemon is a single long-lived process listening on a Unix socket on macOS/Linux (named pipe on Windows) — same idea as `gh auth status` / `ssh-agent`.
- `xpc serve` (the agent), `xpc daemon` (host-side), and `xpc <command>` (CLI) all live in the same source tree and same release artifact, even if the agent has to cross-build for x86 XP.
- Real-VM tests are gated behind a manually-triggered CI workflow + a local `make test-real-vm`. Default `make test` is unit + mock-agent integration only.

---

## 10. Open questions for the user (Phase 0 → Phase 1 gate)

These need answers before architecture sign-off in Phase 1. Grouped by load-bearing-ness.

### 10.1 Load-bearing (architecture-shaping)

1. **Adopt ARCP as the wire protocol for xpc?** The RFC and the use-cases gist were given as required reading; ARCP plugs directly into xpctl's gaps; you authored the RFC. Strong inference: yes. Confirm or correct.
2. **If yes to (1), which ARCP transport for host↔VM?** Options A/B/C in §4. Strong recommendation: **A** (length-prefixed binary framing, TLS, ARCP-shaped envelopes) for v0, with a path to B (WebSocket) later if we want to plug in browser/MCP tooling. Confirm.
3. **Which ARCP RFC version do we build against?** Pin to the current draft on the gist? Tag a frozen `arcp-rfc-2026-05` and treat the spec as immutable for v0?
4. **Persistent host-side daemon: required or optional?** Master prompt §5 leans toward required ("strongly recommend the daemon model"); the simpler thing for Phase 5 is a stateless `xpc <cmd>` that opens a fresh connection each time. I'd prefer the daemon, but ship stateless first and the daemon as a Phase-5b enhancement. OK?
5. **Agent on the VM: keep Python 3.4, or rewrite to .NET 4.0 / C++?** Strong recommendation: **stay in Python 3.4**. It's already on the VM, the existing agent works, and we avoid cross-compilation pain. Confirm.
6. **Agent persistence model in v0: HKLM Run key (current xpctl) or Windows service?** Strong recommendation: **start with Run key, add service install in a later phase.** Run key is what's tested and working today; service install adds risk in Phase 4 with no v0 payoff. Confirm.
7. **Auth model:** PSK + HMAC, mTLS with self-signed certs, or both? Strong recommendation: **PSK + HMAC for v0** because mTLS bootstrap on XP is painful; mTLS as a later upgrade. Confirm.

### 10.2 Scope and packaging

8. **Repo name:** `nficano/xpc` (private) confirmed? (Master prompt says yes; sanity check.)
9. **Backward compatibility with the xpctl agent:** does `xpc` need to talk to a still-running `xpctl` agent, or is it fine to require a fresh `xpc serve` deployment from day one? I'd lean toward "fresh deployment required, with `xpc migrate-from-xpctl` for the host-side config only." Confirm.
10. **Should we keep an SSH transport at all?** xpctl uses SSH for first-boot and as a fallback when the agent is down. With a service-installed agent (eventual) it matters less, but for v0 with a Run-key agent, SSH-as-fallback is genuinely useful. Recommend: **yes, keep SSH, but only for `xpc serve install/start/stop` lifecycle and `xpc bootstrap`, not as a peer transport.**
11. **Are any xpctl subcommands NOT in the master-prompt target list that should still be preserved?** Notable candidates: `xpctl script` (run a local Python file with `client` injected — programmatic API), `xpctl watch` (repeat a command at an interval), `xpctl edit` (edit-remote-file-with-local-editor), `xpctl fetch-exe` (download URL → upload to VM), `xpctl find/head/tail/checksum/cat` (filesystem helpers), `xpctl com list` (COM CLSID dump). Worth keeping all of these? Master prompt doesn't list them — should I treat them as in-scope or out-of-scope for v0?
12. **Host CLI language:** Go (cobra), Rust (clap), Python (click)? Strong recommendation: **Go** — single static binary, best-in-class subcommand ergonomics, easy completion generation, kubectl-quality help text. Python on the host means dragging an interpreter; Rust is fine but Go's stdlib net+TLS+goroutines fit ARCP's streaming model exceptionally well. Confirm.

### 10.3 Operational

13. **Proxmox integration:** master prompt §0/3 mentions `xpc snap` (Proxmox snapshots). What's the Proxmox host address + auth method? xpctl runs `qm` over SSH to a `--proxmox-host` flag — should the host be stored in the profile?
14. **Where does the VM's API key / PSK live?** If we adopt PSK auth (10.7), we need a generation+rotation story. Default: generated by `xpc bootstrap`, stored in `~/.xpc/credentials` and `C:\xpc\agent.key` on the VM. OK?
15. **Should `~/.profile.local` API keys (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`) be used immediately, or are they only for a later phase?** Master prompt says "MCP smoke tests"; no current subcommand needs them in v0 — confirm we can defer.
16. **`MASTER.md` location:** keep the master prompt in the new repo at `MASTER.md` or `docs/MASTER.md`? (For traceability — agents in later phases should be able to quote it.)

---

## 11. Confirmed answers (Phase 0 → Phase 1)

| Q | Decision |
|---|---|
| 10.1.1 Wire protocol | **Adopt ARCP** — RFC 0001 envelope shape |
| 10.1.2 Transport | **Length-prefixed binary framing over TLS-TCP** (option A); WebSocket deferred |
| 10.1.3 ARCP RFC version | Snapshot the gist at Phase 1 sign-off into `docs/protocol/RFC-0001.md`; treat as frozen for v0 |
| 10.1.4 Host daemon | **Stateless v0**, daemon as Phase 5b enhancement |
| 10.1.5 Agent language | **Python 3.4** (keep) |
| 10.1.6 Agent persistence | **HKLM Run key** (xpctl model) for v0; service install deferred |
| 10.1.7 Auth | **PSK + HMAC** (TLS still wraps); mTLS deferred |
| 10.2.8 Repo | `nficano/xpc` private (per master prompt) |
| 10.2.9 xpctl-agent compat | **Fresh `xpc serve` required**; no protocol bridge |
| 10.2.10 SSH role | **Bootstrap + agent lifecycle only**; not a peer transport |
| 10.2.11 Extras to preserve | `script`, `watch`, `edit`, filesystem helpers (`find/head/tail/checksum/cat/fetch-exe`) — all kept, renamed to master-prompt convention |
| 10.2.12 Host CLI language | **Go** (cobra) |
| 10.3.13 Proxmox details | Deferred to `xpc snap` implementation phase |
| 10.3.14 PSK rotation | Auto-generate at `xpc bootstrap`; rotation via `xpc rotate-key` deferred |
| 10.3.15 `~/.profile.local` | Defer to whichever phase first needs MCP |
| 10.3.16 MASTER.md location | Committed at repo root |

### 11.1 Renaming the preserved extras

Top-level, Sysinternals-flat, matching the master-prompt subcommand brevity:

| xpctl source | xpc name |
|---|---|
| `xpctl script <local.py>` | `xpc py local <local.py>` (subcommand of `xpc py`) |
| `xpctl watch <cmd>` | `xpc watch <cmd>` |
| `xpctl edit <vm:path>` | `xpc edit <vm:path>` |
| `xpctl cat <vm:path>` | `xpc cat <vm:path>` |
| `xpctl head <vm:path>` | `xpc head <vm:path>` |
| `xpctl tail <vm:path>` | `xpc tail <vm:path>` |
| `xpctl find <vm:path>` | `xpc find <vm:path>` |
| `xpctl checksum <vm:path>` | `xpc sum <vm:path>` |
| `xpctl fetch-exe <url>` | `xpc fetch <url> [vm:path]` |
| `xpctl com list` | not preserved — `xpc reg get HKCR\CLSID --recurse` covers it |

## 12. Phase 0 exit checklist

- [x] Read xpctl source tree exhaustively.
- [x] Read RFC and use-case gists in full.
- [x] Live VM reachable; OS / Python / debuggers / agent state captured.
- [x] xpctl pain points + keep-list documented.
- [x] xpctl ↔ xpc command-surface mapping documented.
- [x] ARCP-vs-xpctl-gap mapping documented.
- [x] User answers to §10 captured (§11).
- [ ] Migrate this file to `docs/INVESTIGATION.md` once the repo is created in Phase 2.
