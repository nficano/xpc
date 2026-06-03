# xpc Architecture (Phase 1)

> Status: **Awaiting user sign-off.** This is the Phase 1 hard gate per `MASTER.md` §5. No production code is written before approval.
>
> Lives in `/tmp/xpc-investigation/` until Phase 2 creates the `nficano/xpc` repo, then moves to `docs/ARCHITECTURE.md`.
>
> Companion documents: `INVESTIGATION.md` (Phase 0 findings, command-surface mapping, ARCP-vs-xpctl-gap analysis); `protocol/RFC-0001.md` (frozen ARCP RFC snapshot — to be created at sign-off time).

---

## 1. Decision summary

| # | Decision | Choice |
|---|---|---|
| D1 | Host CLI language | **Go 1.22+** with `spf13/cobra` + `spf13/viper` |
| D2 | Agent language / runtime | **Python 3.4.10** (already on the VM) |
| D3 | Wire protocol | **ARCP** envelope shape (RFC 0001), length-prefixed binary framing |
| D4 | Transport | **TLS 1.2 over TCP**, single port (default 9578) |
| D5 | Authentication | **PSK + HMAC-SHA256** message signatures, layered under TLS |
| D6 | Connection model | **Stateless v0** (one TCP conn per `xpc <cmd>`); host-side daemon = Phase 5b |
| D7 | Agent persistence | **HKLM Run key** (xpctl model); service install deferred |
| D8 | Profile / config | **AWS-style split**: `~/.xpc/config` (non-secret INI) + `~/.xpc/credentials` (secret INI) |
| D9 | xpctl compatibility | **Fresh `xpc serve` required.** No protocol bridge. `xpc migrate-from-xpctl` migrates host-side config only |
| D10 | SSH role | **Bootstrap + agent lifecycle only.** Not a peer transport |
| D11 | Subcommand surface | **Sysinternals-flat**, mostly top-level; full list in §10 |
| D12 | TLS trust model | Self-signed cert per VM; **fingerprint pinned** in profile (TOFU on first connect) |

---

## 2. Component map

```
Host (macOS / Linux)                                     XP VM (172.16.20.173)
┌──────────────────────────────┐                         ┌────────────────────────────┐
│  xpc (Go binary)             │                         │  xpc serve (Python 3.4)    │
│  ├─ cobra subcommand tree    │                         │  ├─ ARCP envelope codec    │
│  ├─ ARCP envelope codec      │                         │  ├─ HMAC verify/sign       │
│  ├─ HMAC sign/verify         │                         │  ├─ TLS server (Schannel-  │
│  ├─ TLS client (stdlib)      │  ───── TLS 1.2 ────►    │  │   compatible cipher set)│
│  ├─ profile loader           │      ARCP envelopes      │  ├─ tool registry          │
│  ├─ output formatters        │  ◄──── over TCP ─────   │  │   (exec/cp/reg/...)     │
│  │   (text, json, table)     │     length-prefixed      │  ├─ persistent state:      │
│  └─ stdio output             │                         │  │   ├─ pyshell consoles  │
│                              │                         │  │   ├─ debugger sessions │
│                              │                         │  │   └─ active streams    │
│                              │                         │  └─ optional: HKLM Run key │
└──────────────────────────────┘                         └────────────────────────────┘
        ~/.xpc/config   (profile defaults, fingerprints)      C:\xpc\agent.py
        ~/.xpc/credentials (PSK secret, profile passwords)    C:\xpc\agent.key  (PSK)
        ~/.xpc/state    (active profile pointer)              C:\xpc\agent.pem  (TLS cert)
```

In v0 there is no `xpc daemon` process; every `xpc <cmd>` opens a new TCP connection. Phase 5b adds a host daemon listening on a Unix socket / named pipe to keep one warm TLS connection open.

---

## 3. Decisions in detail

### D1. Host CLI: Go 1.22+ with cobra + viper

**Why.** Single static binary distribution on macOS/Linux/Windows. cobra's subcommand tree, completion generation (`xpc completion bash|zsh|fish|pwsh`), and `--help` rendering are battle-tested in kubectl, gh, hugo. Stdlib `crypto/tls`, `net`, `encoding/json`, and goroutines map cleanly onto ARCP's streaming model.

**Rejected.**
- *Rust + clap.* Equivalent capability, slower iteration. Rejected on velocity grounds for a daily-use tool.
- *Python + click/typer.* Fastest dev velocity but distribution is messy (PyInstaller bundles, slow startup, `sys.executable` confusion on Windows). Doesn't match the "AWS CLI v2 / kubectl" target UX from `MASTER.md` §2.

**Toolchain.**
- Go 1.22 minimum.
- `golangci-lint` for lint (gofmt + vet + staticcheck + revive).
- `go test` + `testify/require` for unit tests.
- `goreleaser` (or simple Make targets) for darwin-amd64 + darwin-arm64 + linux-amd64 + linux-arm64 release artifacts. Windows host as stretch goal per master prompt §11.9.

### D2. Agent: Python 3.4.10 on the VM

**Why.** Already installed. The existing xpctl agent works today and we are converging the surface area, not rebuilding the runtime. Stdlib-only (`socket`, `ssl`, `ctypes`, `subprocess`, `threading`, `winreg`, `code`) — no third-party deps, mirroring xpctl's deploy story. The persistent Python REPL (`code.InteractiveConsole`) is critical for SAPI4/Klatt iteration and is essentially free in Python.

**Rejected.**
- *.NET Framework 4.0 / C#.* Better Win32 ergonomics on paper (services, registry, Schannel TLS) but adds cross-build complexity, slower agent-side iteration, and we don't get a payoff for v0 since we're staying with the Run-key persistence model (D7).
- *C++ Win32.* Smallest deploy, biggest dev cost. No payoff for v0.

**Constraints.**
- Python 3.4's stdlib `ssl` supports TLS 1.2 if the underlying OpenSSL build supports it. The unofficial Python 3.4.10 build for XP currently shipped with xpctl needs to be confirmed in Phase 3 to support TLS 1.2 cipher suites that match modern Go TLS clients.
- ctypes against `kernel32`/`user32`/`ntdll` is XP-32-bit only; we don't have to handle WoW64.
- Agent stays a single `agent.py` file, deployed alongside `agent.key` and `agent.pem`.

### D3. Wire protocol: ARCP envelope (RFC 0001)

**Why.** ARCP closes every protocol-level gap xpctl has today: streaming exec output (`stream.chunk`), cancellation (`cancel`), durable file pulls (`job.checkpoint` + `resume`), tracing across multi-step workflows (`trace_id`, `span_id`, `causation_id`), permission challenges for state-changing ops (`permission.request`/`grant` with `lease_id`), and at-least-once delivery via dedupe-by-`id`. xpctl's wire shape is already very close (`{id, type, action, params, status, data, error}`) — adopting ARCP is closing the gaps, not rewriting from scratch.

**Envelope shape used by xpc** (subset of RFC §6.1):
```json
{
  "arcp":          "1.0",
  "id":            "msg_01HABCDEF",   // ULID; idempotency key
  "type":          "tool.invoke",     // see message types below
  "session_id":    "sess_01HSE...",   // assigned at session.open
  "job_id":        "job_01H...",      // present after job.accepted
  "stream_id":     "str_01H...",      // present for stream.* messages
  "trace_id":      "tr_01H...",       // stable across one xpc <cmd>
  "span_id":       "sp_01H...",
  "correlation_id":"msg_01H...",      // command id this responds to
  "causation_id":  "msg_01H...",
  "timestamp":     "2026-05-08T18:21:00Z",
  "auth":          {"alg":"HMAC-SHA256","kid":"v0","sig":"..."},
  "payload":       { /* type-specific */ }
}
```

**Message types in v0** (subset of RFC §6.2):
- Control: `session.open`, `session.accepted`, `session.close`, `ping`, `pong`, `ack`, `nack`, `cancel`, `permission.request`, `permission.grant`, `permission.deny`. **Deferred:** `resume`, `checkpoint.create`/`restore`, `backpressure`. Adding these is non-breaking later.
- Execution: `tool.invoke`, `tool.result`, `tool.error`, `job.accepted`, `job.started`, `job.progress`, `job.completed`, `job.failed`, `job.cancelled`. **Deferred:** `job.heartbeat`, `job.checkpoint`, `workflow.start`, `agent.delegate`, `agent.handoff`.
- Streaming: `stream.open`, `stream.chunk`, `stream.close`, `stream.error`. All in v0.
- Event: `log` only. **Deferred:** `metric`, `trace.span` — OTel export designed in [RFC 0002](protocol/RFC-0002-otel.md).

**Capability negotiation** at `session.open`:
```json
{ "capabilities": {
    "streaming": true,
    "durable_jobs": false,    // v0 has no durable storage
    "checkpoints": false,     // deferred
    "binary_streams": true,   // for cp / dump / shot
    "agent_handoff": false
}}
```

**Rejected.**
- *xpctl-style request/response.* Re-inventing what ARCP already specifies; no path to MCP integration; would have to retrofit streaming/cancellation later. Master prompt's emphasis on "uniform UX across every subcommand" is satisfied better by a single ARCP-shaped contract.
- *gRPC.* Effectively dead on XP — `grpcio` Python wheels don't exist for 3.4, and modern .NET gRPC is XP-incompatible.
- *JSON-RPC 2.0.* Subset of what ARCP gives. Choosing it would mean re-implementing most ARCP semantics on top of it.

### D4. Transport: TLS 1.2 over TCP, length-prefixed binary framing

**Why.** Same framing as xpctl today (`[4-byte BE uint32 length][UTF-8 JSON payload]`), wrapped in TLS. This deviates from ARCP RFC §17 mandatory transports (WebSocket, stdio); we're trading conformance for pragmatism on XP.

**Framing details.**
- Outer: `[4-byte BE uint32 length][payload bytes]`. `length ≤ 50 MB` (matching xpctl's safety limit).
- Inner: UTF-8 JSON envelope (D3). Binary streams (`stream.chunk` for cp/dump/shot) embed bytes as base64 in payload; **alternative** to evaluate in Phase 3: a second framing channel for binary blobs that bypasses base64 (saves 33%). Decide in Phase 3 spec, not here.
- Per-connection: TLS 1.2 handshake → optional `session.open` exchange → message stream until either side sends `session.close` or the TCP closes.

**TLS configuration.**
- Cipher suites: `ECDHE-RSA-AES256-GCM-SHA384`, `ECDHE-RSA-AES128-GCM-SHA256` minimum. To be confirmed against XP's Python 3.4 OpenSSL build in Phase 3.
- Cert: self-signed, generated at `xpc bootstrap` on the VM, fingerprint pinned in `~/.xpc/config` per profile. First connection prompts TOFU. No CA. No SAN-based hostname verification (we use IP-rooted fingerprint pinning).
- The Go client uses `tls.Config{InsecureSkipVerify: true, VerifyConnection: pinFingerprint}`. The XP Python server uses `ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)`.

**Rejected.**
- *WebSocket over TLS.* Strictly conformant with ARCP RFC §17. Requires a WS library that runs on Python 3.4 on XP (e.g. `websockets`-port or hand-rolled). Defer until v2; gives us a path to plug into MCP/browser tooling later without changing message shape.
- *Cleartext TCP* (xpctl current). Rejected: ARCP envelope already includes `auth.sig`, but without TLS the wire is replayable/snoopable. PSK alone over cleartext leaks message contents; we want both.

### D5. Auth: PSK + HMAC-SHA256, layered under TLS

**Why.** Provisioning a per-VM 32-byte secret is trivial at `xpc bootstrap`; PKI/mTLS would require a CA story that's too much friction for v0. HMAC on every message gives replay-protection (`id` + monotonic timestamp), key-rotation hooks (`auth.kid` field for future rotation), and a clear authorization boundary at the agent.

**Mechanism.**
- `xpc bootstrap` generates a 32-byte random secret. Stores host-side as `psk` in `~/.xpc/credentials` under `[<profile>]`. Stores VM-side as `C:\xpc\agent.key` (file ACL set so only the running user can read).
- Every envelope carries `auth: {alg: "HMAC-SHA256", kid: "v0", sig: hex(hmac(psk, canonical_envelope_minus_auth))}`. Canonicalization spec: deterministic JSON serialization (sorted keys, no extra whitespace) of the envelope minus `auth.sig`.
- Agent verifies HMAC before dispatching. Mismatched `kid` or bad sig → `nack` with `auth_failed` and connection close.
- `session.open` rejected without valid HMAC → no message dispatch.

**Rotation (deferred).**
- `xpc rotate-key`: generate new secret, push to agent under a new `kid` (`v1`), let agent accept both for an overlap window, then retire `v0`. Implement in Phase 6+.

**Rejected.**
- *mTLS with self-signed certs.* Conceptually clean but more code, more failure modes (cert expiry, regen on VM rebuild, file-ACL handling on XP). No payoff vs. PSK on a private LAN.
- *Both layered (mTLS + HMAC).* Defensible but excessive for v0. PSK + TLS gets us replay protection, confidentiality, and authorization with one moving part.
- *No auth.* Present xpctl model. Re-touching the protocol later costs more than doing it once now.

### D6. Connection model: stateless v0, daemon = Phase 5b

**Why.** Master prompt §5 recommends a daemon for latency reasons but allows it to be deferred. Phase 5 critical-path (first working `xpc exec dir C:\`) is shorter without the daemon. ~5 ms LAN latency means a stateless model is acceptable; users running tight loops will appreciate the daemon when we add it.

**v0 contract.** Each `xpc <cmd>` invocation:
1. Loads profile from `~/.xpc/config` and credentials from `~/.xpc/credentials`.
2. Opens a TLS connection to `<host>:<port>`.
3. Sends `session.open` (with capability negotiation).
4. Sends one or more `tool.invoke` (or other commands) per the subcommand.
5. Streams output via `stream.chunk` to local stdout/stderr; collects terminal `tool.result` / `job.completed` / `tool.error`.
6. Sends `session.close`, drops the TCP connection.
7. Returns the exit code.

**Phase 5b daemon.** A long-lived host process (`xpc daemon`) listening on `~/.xpc/run/daemon.sock`. CLI invocations connect via the socket; daemon multiplexes their requests onto one warm TLS connection per profile. Backwards compatible: if the daemon isn't running, the CLI falls back to direct connect.

**Rejected.**
- *Daemon required from Phase 5.* Pushes "first working subcommand" later. Adds Phase 5 risk for no Phase-5 payoff.
- *Skip the daemon entirely.* Tight loops over the LAN suffer; future MCP-style scenarios need persistent context.

### D7. Agent persistence: HKLM Run key

**Why.** xpctl uses this today and it works. Service install on XP is finicky and adds debug surface in Phase 4. Run key is sufficient because the VM is single-user and is left logged in (typical RE/automation lab setup).

**Mechanism (mirrors xpctl).**
- Registry value: `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Run\xpc_agent` = `"C:\Python34\python.exe" "C:\xpc\agent.py" --port 9578`.
- `xpc serve install-startup` / `remove-startup` / `startup-status` — direct port of xpctl's actions.
- Service install (`xpc serve install --as-service`) is a Phase 6+ option, not v0.

**Rejected for v0.** Service install (more work, no payoff). Both modes (more code paths to test).

### D8. Profile / config: AWS-style split

**Layout.**
```
~/.xpc/
├── config         # non-secret INI: hosts, ports, transport flags, fingerprints
├── credentials    # secret INI: PSK secrets, optional SSH passwords
├── state          # active profile pointer (single line, just the profile name)
└── run/           # Phase 5b: daemon socket + pid file
```

**`~/.xpc/config` schema (per profile):**
```ini
[profile lab]
host = xp-truvoice-w02
port = 9578
fingerprint = sha256:AB:CD:...   # pinned at first connect
proxmox_host =                    # optional, for xpc snap
proxmox_user =                    # optional
ssh_user = cyg_server             # used only by xpc bootstrap and xpc serve lifecycle
verify_host_key = true
```

**`~/.xpc/credentials` schema (per profile):**
```ini
[profile lab]
psk = base64-encoded 32-byte secret
ssh_password = ...                # optional, only if PSK-based SSH not configured
```

**`~/.xpc/state`:**
```
default
```

**`xpc configure`** behavior:
- Aws-style interactive prompts for host/port/ssh_user.
- Validates connection via TLS handshake + `ping`.
- If cert is unknown: TOFU prompt (display fingerprint, ask Y/n to pin).
- Writes both files with `0o700`/`0o600` perms.

**`xpc migrate-from-xpctl`** behavior:
- Reads `~/.xpcli/config`. For each `[<name>]`, write `[profile <name>]` to `~/.xpc/config` (with `host`, `port`, `ssh_user`, `verify_host_key`) and `[profile <name>]` to `~/.xpc/credentials` (with `ssh_password`). Does not generate PSK; user runs `xpc bootstrap <profile>` to deploy a new agent and key.

**Env vars** (mirroring xpctl): `XPC_PROFILE`, `XPC_HOST`, `XPC_PORT`, `XPC_SSH_USER`, `XPC_SSH_PASSWORD`. Override config file values; credentials file values take precedence over env-var passwords (so secrets aren't accidentally exposed via shell history).

### D9. xpctl compatibility: fresh `xpc serve` required

**Implication.** No protocol bridge. The `xpc` protocol library has one parser. The legacy xpctl agent on the VM is replaced during `xpc bootstrap`. `xpc migrate-from-xpctl` only handles host-side config — VM-side state is replaced.

**Migration UX.** `xpc bootstrap <profile>`:
1. Connects via SSH (using migrated profile credentials).
2. Stops the xpctl agent (graceful via TCP `agent_shutdown`, fall back to WMIC kill via SSH).
3. Removes `C:\xpctl\` (optional, behind a confirmation flag).
4. Deploys `agent.py`, `agent.key` (newly generated PSK), `agent.pem` (newly generated TLS cert) to `C:\xpc\`.
5. Registers `xpc_agent` in HKLM Run key.
6. Starts the agent.
7. Waits for ARCP `ping` to succeed.
8. Persists fingerprint into `~/.xpc/config`.

### D10. SSH role: bootstrap + agent lifecycle only

**Why.** The agent is the canonical control plane for v0. SSH exists for:
1. **First-boot bootstrap** when no agent is yet deployed.
2. **Agent lifecycle**: start/stop/redeploy when the agent is dead or being upgraded.
3. **Recovery** when the agent is wedged.

For day-to-day commands (`exec`, `cp`, `reg`, `ps`, etc.), the protocol library refuses to send over SSH — there's no SSH transport in the agent code path. This drops a large quantity of ssh_support glue from the codebase.

**Mechanism.** The host-side SSH path uses the local `ssh` binary with `paramiko` as a fallback (matching xpctl's robustness). Subset of operations: file push/pull (via SCP), remote command exec (cmd.exe), starting/killing the agent process. Implemented in the Go host as a thin `internal/sshlife` package, used only by `xpc bootstrap` and `xpc serve {install,start,stop,redeploy}`.

### D11. Subcommand surface

See §10 for the full locked surface. Sysinternals-flat: short top-level commands rather than deep groups. Exceptions: `xpc agent <op>` (lifecycle), `xpc reg <op>`, `xpc bat <op>`, `xpc py <op>`, `xpc snap <op>`, `xpc dbg <op>`, `xpc trace <op>`, `xpc dll <op>`, `xpc gui <op>`, `xpc svc <op>`, `xpc net <op>`, `xpc evt <op>` — each because the subcommand has clearly-related operations that benefit from grouping. Everything else is top-level.

### D12. TLS trust: fingerprint pinning, TOFU on first connect

**Why.** No CA infrastructure on a personal LAN. `xpc bootstrap` generates a self-signed cert on the VM. First `xpc <cmd>` against a profile compares the cert against the pinned fingerprint in the profile; if absent, TOFU prompt; if present and mismatching, refuse and surface the change.

**Cert rotation** uses the same prompt: `xpc bootstrap --regenerate-cert` regenerates and re-pins.

---

## 4. End-to-end: `xpc exec dir C:\` (first working command)

```
xpc exec dir 'C:\'
│
├── 1. Load profile 'default' from ~/.xpc/config and ~/.xpc/credentials
├── 2. Open TCP/TLS to xp-truvoice-w02:9578
├── 3. Verify TLS cert fingerprint against pinned value in profile
├── 4. session.open  { capabilities: {streaming, binary_streams} }   [HMAC signed]
│       ◄── session.accepted  { session_id, agent.capabilities }
├── 5. tool.invoke   { tool: "exec", arguments: {cmd: "dir C:\\", shell: "cmd"} }   [HMAC]
│       ◄── job.accepted   { job_id, correlation_id }
│       ◄── job.started    { job_id }
│       ◄── stream.open    { stream_id, content_type: "text/plain", channel: "stdout" }
│       ◄── stream.chunk   { stream_id, delta: "Volume in drive C is..." }
│       ◄── stream.chunk   { ... }
│       ◄── stream.close   { stream_id }
│       ◄── tool.result    { exit_code: 0 } | tool.error { code, message }
│       ◄── job.completed  { state: "completed" }
├── 6. session.close
└── 7. Return exit code 0 to shell
```

This becomes the Phase 5 exit-gate test, run against the real VM.

---

## 5. Phase exit gates (locked)

| Phase | Exit condition |
|---|---|
| **0** | INVESTIGATION.md complete; user signed off on §10 answers. ✅ |
| **1** | This doc signed off. ⏳ |
| **2** | `nficano/xpc` private repo exists; `TASKS.md` populated from MASTER.md + this doc; CI green on scaffold; branch protection on |
| **3** | Real-network ARCP round-trip passes against the XP VM (host stub ↔ agent stub) |
| **4** | `xpc serve` deployable + installable as Run-key startup; test client gets `dir C:\` output back |
| **5** | `xpc exec dir 'C:\'` end-to-end against real VM; bash + zsh completion work; `xpc configure` works |
| **5b** (optional) | `xpc daemon` warm-connection multiplex working |
| **6+** | Per subcommand: unit + integration + real-VM tests green, PR merged, TASKS.md + CHANGELOG updated |

---

## 6. Risks register

| # | Risk | Likelihood | Mitigation |
|---|---|---|---|
| R1 | Python 3.4 OpenSSL doesn't support TLS 1.2 with cipher suites Go expects | Medium | Phase 3 includes a real-VM TLS handshake test; fallback to TLS 1.0 with HMAC carrying the security if needed |
| R2 | ARCP RFC drifts during construction | Medium | Snapshot RFC at architecture sign-off into `docs/protocol/RFC-0001.md`; treat as immutable for v0 |
| R3 | Bootstrap mirror (Cygwin 2016 snapshot) goes offline | Low | Bundle `cygwin-2.874.exe` in repo; host installer assets in `installs/` like xpctl does |
| R4 | XP VM rebuild loses pinned fingerprint and PSK; `xpc <cmd>` fails opaque | Low | `xpc bootstrap --regenerate` regenerates both; document recovery in CONTRIBUTING |
| R5 | base64-in-JSON is too slow / memory-heavy for large `xpc dump` (process minidump) pulls | Medium | Phase 3 evaluates a binary-channel framing alternative; if slow, add it before `xpc dump` lands |
| R6 | Run-key persistence loses agent on RDP/VNC disconnect | Low (existing xpctl behavior) | Future: service-install mode behind `--as-service` flag |
| R7 | Stateless v0 connection setup latency hurts in tight loops | Low | Phase 5b daemon resolves it; v0 acceptable on LAN (~10 ms TLS handshake) |
| R8 | XP-side Python 3.4's `ssl` module doesn't support `verify_mode=CERT_NONE` + cert validation we need | Medium | Phase 3 spike confirms; alternative is HMAC-only on cleartext + pinned IP if TLS proves intractable (we'd downgrade D4) |
| R9 | Cobra completion script for fish/pwsh has known rough edges | Low | Ship bash + zsh first; fish/pwsh as best-effort with documented gaps |
| R10 | Sub-agent dispatch in Phase 6+ creates merge conflicts on shared infra | Medium | Phases 1-5 stay sequential; sub-agent dispatch only after the protocol & daemon are frozen |

---

## 7. Repo skeleton (target for Phase 2)

```
xpc/
├── MASTER.md                 # the master prompt, verbatim
├── README.md
├── CHANGELOG.md
├── LICENSE                   # MIT (matching xpctl)
├── CONTRIBUTING.md
├── SECURITY.md
├── TASKS.md                  # source of truth, populated in Phase 2
├── go.mod
├── go.sum
├── Makefile
├── .github/workflows/
│   ├── ci.yml                # lint + test on PR
│   ├── release.yml           # tag-driven goreleaser
│   └── real-vm-test.yml      # manual dispatch only
├── .pre-commit-config.yaml
├── docs/
│   ├── INVESTIGATION.md       # moved from /tmp/xpc-investigation/
│   ├── ARCHITECTURE.md        # this doc
│   ├── PROTOCOL.md            # written in Phase 3
│   ├── protocol/
│   │   └── RFC-0001.md        # frozen ARCP snapshot
│   ├── SPEC-<subcommand>.md   # one per subcommand starting Phase 6
│   └── sessions/              # real-VM session logs
├── cmd/
│   └── xpc/                   # main package (host CLI binary)
├── internal/
│   ├── arcp/                  # envelope codec, message types, HMAC, capability negotiation
│   ├── transport/             # TLS+TCP framing
│   ├── profile/               # ~/.xpc/{config,credentials,state}
│   ├── output/                # text|table|json formatters
│   ├── sshlife/               # SSH lifecycle helpers (bootstrap, install, kill)
│   └── cli/                   # cobra commands, one file per subcommand
├── agent/
│   ├── agent.py               # the on-VM agent (single file, Python 3.4-compatible)
│   ├── tests/                 # python tests for agent dispatch + protocol roundtrip
│   └── scripts/               # helper python scripts the agent uses (mem_read, dll_inject, etc.)
├── installs/                  # bundled Python/Cygwin/OllyDbg/x64dbg archives (mirrors xpctl)
└── tests/
    ├── integration/           # mock-agent-in-process integration tests
    └── real_vm/               # real-VM test fixtures (manual workflow only)
```

---

## 8. Testing strategy

Per `MASTER.md` §12: three layers, all required.

1. **Unit (Go + Python).** Pure-function tests on envelope encode/decode, HMAC, profile loading, output formatters. Property-based tests via `gopter` (Go) and `hypothesis` (Python) on the wire format roundtrip.
2. **Integration.** A mock agent (Python `agent.py` running in a subprocess on the host CI) talking to the Go CLI over a Unix socket / `localhost:0`. Fast, deterministic, runs on every PR.
3. **Real-VM.** A manually-triggered CI workflow plus a `make test-real-vm` target that points at the live VM in `~/.xpc/config`. Required green before tagging a release; not required on every PR.

CI on PR: lint + unit + integration. CI on tag push: above plus release artifacts. Manual `real-vm-test.yml` workflow: any time, gated by approval.

---

## 9. Open items deferred to later phases (informational)

Not blockers for Phase 1 sign-off.

- Proxmox API host + auth for `xpc snap` — gathered when Phase 6+ implements the subcommand.
- PSK rotation UX (`xpc rotate-key`) — Phase 6+.
- WebSocket transport — possible v2 if MCP integration becomes interesting.
- Service install (`xpc serve install --as-service`) — Phase 6+.
- OpenTelemetry export — designed in [RFC 0002](protocol/RFC-0002-otel.md) (host-side OTLP producer).
- `xpc daemon` host-side multiplex — Phase 5b.
- Argv[0]-based shims (`xpcexec`, `xpcreg`) — after dispatcher is solid.
- Binary-channel framing alternative to base64-in-JSON for big binary streams — evaluated in Phase 3.

---

## 10. Locked subcommand surface

Top-level (Sysinternals-flat). Items marked "extra" are preserved from xpctl and renamed per Phase 0 §11.1.

```
# Setup / lifecycle
xpc configure                              # interactive profile setup
xpc bootstrap [<profile>]                  # first-boot deploy of agent + key + cert
xpc migrate-from-xpctl                     # one-shot migration of ~/.xpcli/config
xpc completion bash|zsh|fish|pwsh
xpc rotate-key                             # deferred to Phase 6+

xpc profile list|add|remove|use
xpc use <profile>

# Agent lifecycle
xpc serve [--port N]                       # the agent itself; runs on the XP VM
xpc serve install-startup|remove-startup|startup-status
xpc agent ping|status|info
xpc agent deploy|start|stop|redeploy
xpc agent install|uninstall                # full install: deploy + start + register
xpc daemon                                 # Phase 5b

# Core remoting
xpc exec <cmd> [args...]                   # streaming stdout/stderr/exit
xpc cp <src> <dst>                         # bidirectional, host:/vm: prefixes
xpc bat run|push-run|create
xpc reg get|set|delete|export
xpc py run|repl|pip|local                  # `local` is the renamed `xpctl script`
xpc tun -L|-R port:host:port               # ARCP-multiplexed tunnels

# System / info
xpc info                                   # systeminfo
xpc ps
xpc net netstat|ipconfig|route             # `xpc net` summary if no subcommand
xpc svc list|start|stop|install|uninstall|status
xpc env list|set
xpc evt tail|query
xpc boot reboot|shutdown|pause|resume

# RE-focused
xpc dll list|inject|regsvr32
xpc dump <pid> [--full]
xpc inj <pid> <dll>                        # alias of `xpc dll inject`
xpc shot                                   # gui screenshot
xpc send keys|click|move
xpc gui window-list                        # remaining GUI helper
xpc dbg attach|run|server                  # OllyDbg / WinDbg(CDB) / x64dbg
xpc trace start|stop|pull
xpc ghidra start|stop
xpc ida start|stop
xpc snap list|create|restore|delete

# Filesystem / coreutils-flavored extras (preserved from xpctl, renamed)
xpc cat <vm:path>
xpc head <vm:path>
xpc tail <vm:path>
xpc find <vm:path>
xpc sum <vm:path>
xpc fetch <url> [vm:path]                  # download URL → upload to VM
xpc edit <vm:path>                         # download → $EDITOR → re-upload
xpc watch <cmd>                            # repeat cmd at interval
```

Universal flags (per `MASTER.md` §11): `--profile`, `--target`, `--output {text|table|json}`, `--dry-run` (state-changing only), `-v/--verbose`, `--timeout`. Exit codes: `0` ok, `1` generic, `2` usage, `3` connection, `4` auth, `5` remote command (with remote exit propagated where sensible).

---

## 11. Approval checklist

Phase 1 closes when the user confirms each row.

- [ ] D1 Go + cobra
- [ ] D2 Python 3.4 agent
- [ ] D3 ARCP envelopes
- [ ] D4 TLS-TCP, length-prefixed binary framing
- [ ] D5 PSK + HMAC-SHA256
- [ ] D6 Stateless v0, daemon Phase 5b
- [ ] D7 HKLM Run key persistence
- [ ] D8 `~/.xpc/{config,credentials,state}` AWS-style split
- [ ] D9 Fresh `xpc serve`; no compat bridge
- [ ] D10 SSH only for bootstrap + lifecycle
- [ ] D11 Subcommand surface in §10
- [ ] D12 Self-signed TLS + TOFU + fingerprint pinning
- [ ] §6 Risks acknowledged
- [ ] §10 surface acceptable; nothing to add or remove
- [ ] Repo skeleton in §7 acceptable

When approved, I will:
1. Create `nficano/xpc` private GitHub repo (Phase 2).
2. Move INVESTIGATION.md and ARCHITECTURE.md from scratch into `docs/`.
3. Commit `MASTER.md` at repo root.
4. Snapshot the current ARCP RFC text into `docs/protocol/RFC-0001.md`.
5. Populate `TASKS.md` from this doc + `MASTER.md`.
6. Configure CI and branch protection.
7. Hand control back for Phase 3 (wire protocol foundation).
