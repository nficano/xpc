# xpc — Master Development Prompt

> **Single source of truth for this project's plan.** This document directs an autonomous coding agent through the full development of `xpc`, the modern successor to `xpctl`. Read it end-to-end before taking any action.

---

## 0. Mission

Build **xpc**, a Sysinternals-style remote management toolkit for Windows XP virtual machines, modeled architecturally on the AWS CLI and `kubectl`: a single dispatcher binary with subcommands, named connection profiles, shell tab completion, persistent connection pooling, and a uniform UX across every subcommand.

This replaces an existing working tool (`xpctl`) with a more elegant, more tested, and more extensible architecture. The existing tool is your reference implementation — not your blueprint.

The deliverable is a production-grade tool that the author (Nick) will use daily for reverse-engineering work on Windows XP VMs (notably the TruVoice/SAPI4 Klatt synthesizer project). It must be **comprehensively tested against a real XP VM** at every phase. Mocked-only verification is not acceptable.

---

## 1. Methodology

This project does **not** use BMAD. It uses the following pattern, in this order, with hard gates between phases:

1. **Investigation first.** Before any architectural decision, read every existing artifact (the prior tool, the RFC, the use cases, the live config). Produce a written investigation report with explicit unknowns surfaced as questions for the user.
2. **Architecture decision with explicit gate.** After investigation, propose 2–3 architecture options with tradeoffs. Wait for the user to approve one. Do not write production code before this gate clears.
3. **Granular task tracker as single source of truth.** Every task — completed, in-progress, blocked, or planned — lives in `TASKS.md` at the repo root. Update on every change. The agent treats this file as authoritative; the user treats this file as the project status board.
4. **Phased TDD.** Each phase has: a written specification, tests written before implementation, implementation, real-VM verification, and a hard exit gate before the next phase begins.
5. **Real-VM verification at every gate.** Every phase ends with a verification command run against the real XP VM defined in `/Users/nficano/.xpcli/config`. If the verification fails, the phase is not done.
6. **Sub-agent dispatch only where parallelism is real.** Don't fan out work that has sequential dependencies. Do fan out independent subcommand implementations once the protocol and CLI scaffolding are stable.

---

## 2. Resources & References

Read these in this order, in full, before doing anything else:

| Resource | Location | What to extract |
|---|---|---|
| RFC (draft) | https://gist.github.com/nficano/202217ea10b243a5f12a7a38d1efa102 | Authoritative protocol design intent |
| Use cases | https://gist.github.com/nficano/f612554c3582014bbd8b735da469179f | Concrete user workflows that must be supported |
| Existing tool (Python) | `~/code/xpctl` | Working reference implementation; protocol semantics; subcommand surface |
| Live connection profile | `/Users/nficano/.xpcli/config` | Profile schema (replicate or improve), VM connection details for testing |
| API keys for MCP testing | `~/.profile.local` | `OPENAI_API_KEY`, `ANTHROPIC_API_KEY` — use these only for MCP smoke tests, never commit them |

Additionally, study these external references during the architecture phase:

- AWS CLI v2 source: https://github.com/aws/aws-cli (profile system, command tree, completion generation)
- kubectl: https://github.com/kubernetes/kubectl (subcommand UX, output formatting flags, plugin model)
- spf13/cobra: https://github.com/spf13/cobra (if Go is chosen for the host)
- Sysinternals PsTools (download and observe their flag conventions, output style, exit codes)

---

## 3. Subcommand Surface (target)

All previously-named tools collapse into subcommands of a single dispatcher binary called `xpc`. Final command surface:

```
xpc configure                      # AWS-style interactive profile setup
xpc profile list|add|remove|use    # profile management
xpc use <profile>                  # set default active profile
xpc completion bash|zsh|fish|pwsh  # generate shell completion

xpc exec <cmd> [args...]           # run command on VM (psexec analog)
xpc cp <src> <dst>                 # bidirectional file copy (host:path / vm:path)
xpc reg get|set|delete|export ...  # registry operations
xpc bat <file.bat>                 # batch file invocation with stream
xpc py run|repl|pip ...            # Python interaction on VM
xpc tun -L|-R port:host:port       # port forwarding
xpc boot reboot|shutdown|pause|resume
xpc snap list|create|restore|delete  # Proxmox snapshot integration
xpc ps                             # process list
xpc svc list|start|stop|install|uninstall  # Windows services
xpc dll <pid>|<exe>                # loaded modules / imports / exports
xpc dump <pid> [--full]            # process dump pulled to host
xpc inj <pid> <dll>                # DLL injection
xpc shot                           # screenshot to host
xpc send keys|click|move ...       # synthetic input
xpc evt tail|query                 # event log
xpc info                           # systeminfo
xpc net                            # ipconfig + netstat + route summary
xpc dbg attach|run|server          # WinDbg/cdb wrapper + dbgsrv lifecycle
xpc trace start|stop|pull          # procmon/API Monitor wrapper
xpc ghidra start|stop              # ghidra_server lifecycle + tunnel
xpc ida start|stop                 # IDA remote debug stub lifecycle + tunnel

xpc serve                          # the agent itself; runs on the XP VM
xpc daemon                         # host-side connection-multiplexing daemon
```

Argv[0] dispatching: ship symlink/shim binaries (`xpcexec`, `xpcreg`, etc.) that exec `xpc` with the subcommand prepended, so the PsTools-style invocation also works for nostalgia/scripting. Implement this only after the dispatcher works.

---

## 4. Phase 0 — Investigation (start here)

**Goal:** Produce enough understanding to make architecture decisions confidently.

**Tasks (track each in `INVESTIGATION.md` as you complete them):**

1. Clone or `cd` into `~/code/xpctl`. Read every source file. Produce a written summary of:
   - Current wire protocol (framing, encoding, auth)
   - Subcommand surface and which are well-implemented vs. half-implemented
   - Connection config format (compare to `/Users/nficano/.xpcli/config`)
   - Test coverage and gaps
   - Pain points and design decisions you would change
2. Read `/Users/nficano/.xpcli/config`. Document the schema. Note any fields you'd add for a profile-aware design.
3. Fetch and read both gists in full. Quote key requirements verbatim into `INVESTIGATION.md`.
4. Identify the live XP VM connection details from the config. Verify the VM is reachable (ping, port test) before proceeding. Document its OS build, Python version (if any), .NET Framework version, available tools.
5. Enumerate **unknowns and open questions** as a checklist at the bottom of `INVESTIGATION.md`. Examples to consider:
   - Does the existing agent run as a service or a console process?
   - Is there existing TLS or is it cleartext TCP?
   - What auth mechanism (token, cert, none)?
   - Are there any subcommands in `xpctl` not in the target list above that should be preserved?
   - What's the Proxmox API endpoint and auth for `xpc snap`?
   - Is there an existing `dbgsrv`/`ghidra_server` setup on the VM?
   - Does the user want backward-compatibility with `xpctl`'s on-VM agent, or a clean replacement?
6. Surface every unknown to the user **before** the architecture phase. Do not guess.

**Phase 0 exit gate:** `INVESTIGATION.md` is committed (locally; repo doesn't exist yet) and the user has answered all open questions in writing.

---

## 5. Phase 1 — Architecture Decision (HARD GATE)

**Goal:** Pick the language(s), wire protocol, auth model, and deployment story. No code yet.

**Required decisions, each with rationale:**

1. **Host CLI language.** Strong candidates: Go (cobra/viper), Rust (clap), Python 3.12 (click/typer). Compare against criteria: single-binary distribution, shell completion ergonomics, gRPC/JSON-RPC support, dev velocity for the user.
2. **Agent language and runtime.** Strong candidates: C# .NET Framework 4.0 (last official XP target), C++ Win32 with MinGW or older MSVC, Python 3.4 (last XP-supported, matches xpctl). Compare against criteria: native Win32 access (registry, services, screenshot, sendkeys, DLL injection), deploy size, install complexity, debugging tools available on XP.
3. **Wire protocol.** Strong candidates: JSON-RPC over TLS-TCP, MessagePack-RPC over TLS-TCP, custom framed binary protocol, gRPC (warning: poor .NET Framework 4.0 support). Must support: streaming stdout/stderr, binary file transfer, port forwarding tunnels, request cancellation.
4. **Authentication.** Pre-shared key with HMAC, mutual TLS with self-signed certs, or both. Define key rotation story.
5. **Connection multiplexing.** Single TCP per command vs. persistent host-side daemon (`xpc daemon`) holding the connection. Strongly recommend the daemon model for latency reasons; confirm with user.
6. **Profile schema.** Migration path from `xpctl`'s config format. Define new schema. Define `xpc configure` interactive flow.

**Deliverable:** `docs/ARCHITECTURE.md` with each decision, rationale, rejected alternatives, and risks.

**Phase 1 exit gate:** User explicitly approves the architecture document. **No code is written before this approval.**

---

## 6. Phase 2 — Repo & Task Tracker Setup

**Goal:** Get the project's source-of-truth infrastructure in place.

**Tasks:**

1. Create a **private** GitHub repo: `nficano/xpc`. Use `gh` CLI:
   ```
   gh repo create nficano/xpc --private --description "Modern remote management toolkit for Windows XP VMs (xpctl successor)"
   ```
2. Initialize: `README.md`, `.gitignore`, `LICENSE` (match user's existing OSS conventions), `CHANGELOG.md`, `docs/` directory containing the investigation and architecture documents from phases 0–1.
3. Create `TASKS.md` at repo root. Schema:

   ```markdown
   # xpc Task Tracker

   > Single source of truth for all project work. Updated on every change.

   ## Legend
   - [ ] Not started
   - [~] In progress
   - [x] Done
   - [!] Blocked (annotate with reason)
   - [?] Needs user input

   ## Phase 0: Investigation
   - [x] Read xpctl source
   ...

   ## Phase 1: Architecture
   ...

   ## Current focus
   <one-paragraph description of what's being worked on right now>

   ## Open questions for user
   <bullet list, each with date raised>

   ## Recently completed (last 7 days)
   <bullet list>
   ```

4. Set up CI:
   - GitHub Actions workflow on PRs: lint, type-check, unit tests, integration tests against a mock agent
   - Optional: a manually-triggered workflow that runs the full test suite against the real XP VM (gated by a manual approval, since it requires the VM to be online)
5. Set up pre-commit hooks for whatever language is chosen (gofmt+golangci-lint, or rustfmt+clippy, or ruff+mypy).
6. Configure branch protection: PRs required, CI green required, no direct push to main.

**Phase 2 exit gate:** Repo exists, `TASKS.md` is populated with every task from this document broken into granular checkboxes, CI passes on an empty scaffold, branch protection is on.

---

## 7. Phase 3 — Wire Protocol Foundation

**Goal:** A protocol library on both sides with round-trip-tested message exchange. No business logic yet.

**Tasks:**

1. Specify the protocol in `docs/PROTOCOL.md`:
   - Message framing (length-prefixed, varint, etc.)
   - Message envelope (id, type, method, payload, error, status)
   - Streaming semantics (server-to-client stdout chunks, client cancellation)
   - File transfer chunking and resumption
   - Tunnel multiplexing (multiple concurrent forwards over one TCP)
   - Auth handshake
   - TLS configuration (cipher suites that work on .NET 4.0 / Schannel on XP — research this; XP+SP3 caps at TLS 1.0 or with hotfixes 1.2)
2. Generate or write a wire format test corpus: example messages encoded as bytes, with a reference decoder check on both sides.
3. Implement the protocol library on the host (in chosen language) with:
   - Encode/decode with property-based tests
   - Mock transport for unit testing
   - Full coverage of message types
4. Implement the protocol library on the agent (in chosen language) with the same test corpus passing.
5. Write a real-network round-trip test: tiny host-side binary opens a TCP connection to a tiny agent-side binary, exchanges every message type, validates byte-for-byte.

**Phase 3 exit gate:** Round-trip test passes against the real XP VM (the test agent stub deployed on the VM, the test host stub run locally). Test corpus committed. Protocol doc complete.

---

## 8. Phase 4 — Agent Core (`xpc serve`)

**Goal:** A working agent on the XP VM that can be installed as a service, accepts authenticated connections, and dispatches a stub `exec` command.

**Tasks:**

1. Service install/uninstall (`xpc serve --install`, `--uninstall`). On .NET, use `ServiceBase` + `sc.exe`. On C++, use `CreateService` Win32 API. On Python 3.4, use `pywin32`.
2. Connection accept loop with TLS handshake.
3. Auth: PSK or mTLS as decided.
4. Command dispatcher with a single registered command: `exec`.
5. `exec` implementation: spawn child process, stream stdout/stderr/exit code back as protocol messages.
6. Logging to a file on the VM (rotatable, capped size).
7. Graceful shutdown on service stop.
8. Crash recovery: agent restarts itself if a handler throws.
9. Build artifact: a single deployable `.exe` (ILMerge for .NET, static link for C++, PyInstaller for Python).

**Phase 4 exit gate:** From the host, you can deploy `xpd.exe` to the VM (manually copy is fine for now), install it as a service, and a test client (still using protocol library directly, not the full CLI yet) runs `dir C:\` on the VM and gets correct output back.

---

## 9. Phase 5 — Host CLI Core (`xpc`) + `exec` end-to-end

**Goal:** The dispatcher binary with profile management, connection daemon, and the `exec` subcommand fully working end-to-end.

**Tasks:**

1. Command tree scaffolding (cobra/clap/click subcommands), help text, version command, global flags (`--profile`, `--target`, `-v/--verbose`, `--output json|table|text`).
2. `xpc configure` interactive prompt. Migrate from `~/.xpcli/config` if present. New config at `~/.xpc/config` and `~/.xpc/credentials` (AWS-style split).
3. `xpc profile list|add|remove|use`. State file for active profile (`~/.xpc/state` or symlinked default).
4. `xpc daemon` host-side multiplexing service (optional flag to disable for stateless mode). Unix socket on macOS/Linux, named pipe on Windows.
5. `xpc exec` implementation talking through the daemon to the agent.
6. `xpc completion <shell>` generation. Test bash, zsh, fish.
7. Output formatting: respect `--output json` everywhere from day one. Default is human-readable.
8. Exit codes: 0 success, 1 generic error, 2 usage error, 3 connection error, 4 auth error, 5 remote command error (with remote exit code propagated when sensible).

**Phase 5 exit gate:** `xpc exec dir C:\` against the real VM produces identical output to `xpctl`'s equivalent. `xpc --help` is clean and AWS-style. Tab completion works in bash and zsh. The `xpc configure` flow successfully sets up a new profile.

---

## 10. Phase 6+ — Subcommand Implementation Loop

**Goal:** Implement every remaining subcommand. Each subcommand is its own mini-phase with its own gate. The agent generates a fresh focused prompt for each subcommand using the **per-subcommand prompt template** below.

**Order of implementation** (chosen so each builds on capabilities the prior added):

1. `xpc cp` — file transfer (validates binary streaming)
2. `xpc reg` — registry ops (validates structured args/output)
3. `xpc info` / `xpc net` — read-only diagnostics (low risk, builds confidence)
4. `xpc ps` / `xpc svc` — process/service management (state-changing)
5. `xpc evt` — event log
6. `xpc shot` / `xpc send` — GUI bridge
7. `xpc bat` — batch invocation
8. `xpc tun` — port forwarding (validates tunneling layer)
9. `xpc py` — Python REPL/exec (validates persistent stream)
10. `xpc dll` / `xpc dump` / `xpc inj` — RE-focused tools
11. `xpc boot` / `xpc snap` — VM lifecycle (Proxmox API integration)
12. `xpc dbg` — WinDbg/cdb wrapper (depends on tunneling)
13. `xpc trace` — procmon wrapper (depends on file pull)
14. `xpc ghidra` / `xpc ida` — RE server lifecycle (depends on tunneling)

After phase 5 is green, fan-out **carefully**. Subcommands 1–5 should be sequential (still building shared infra). 6 onward can be parallelized into sub-agents if the user is comfortable, with one sub-agent per subcommand and a coordinator agent merging.

### Per-subcommand prompt template

When the agent is ready to start a new subcommand phase, it generates a prompt of this form, presents it to the user, and waits for the user to invoke it in a fresh session:

```markdown
# xpc <SUBCOMMAND> implementation prompt

## Pre-reqs
- Master prompt: <link or path>
- TASKS.md current state at commit <sha>
- Phases 0–5 complete and green

## Goal
Implement `xpc <subcommand>` per the spec in docs/SPEC-<subcommand>.md.

## Investigation tasks (gate before implementation)
- Review how xpctl implements the equivalent (or note absence)
- Review Sysinternals' equivalent tool's flag surface and output
- Identify XP-specific gotchas (registry redirection, WoW64, ACL quirks, etc.)
- Document in docs/SPEC-<subcommand>.md

## Test plan (write before implementation)
- Unit tests for arg parsing, output formatting
- Mock-agent integration tests for protocol exchange
- Real-VM tests for actual behavior; list each

## Implementation tasks
- Agent-side handler
- Host-side subcommand
- Output formatters (text + json)
- Help text and examples
- Update TASKS.md
- Update CHANGELOG.md

## Exit gate
- All tests pass in CI
- Manual real-VM run succeeds and is recorded as a session log in docs/sessions/
- TASKS.md updated
- PR opened, self-reviewed, merged

## Sub-agent dispatch (if applicable)
<only if work is genuinely parallelizable; otherwise omit>
```

The agent always generates the next subcommand's prompt at the end of the current phase.

---

## 11. Universal Rules

These apply to every phase, every PR, every task.

1. **TDD is mandatory.** Tests are written before implementation. PRs that ship implementation without prior tests are rejected.
2. **Real-VM verification is mandatory at every gate.** A phase is not done if the only evidence is unit tests.
3. **TASKS.md is updated in the same commit as the work it describes.** Stale task tracker = broken trust in the system.
4. **No silent guesses.** When an unknown is encountered, the agent stops, adds the question to `TASKS.md` under "Open questions for user", and surfaces it.
5. **Every subcommand supports `--output json`** from day one. Do not retrofit later.
6. **Every subcommand has a `--dry-run`** for any state-changing operation.
7. **Idempotency where possible.** `xpc reg set` should be idempotent. `xpc svc start` should be idempotent.
8. **No secrets in logs, ever.** Auth tokens, paths containing usernames, environment variables — scrub before log emission.
9. **Cross-platform host CLI.** Must build and test on macOS (primary dev) and Linux. Windows host support is a stretch goal.
10. **Sysinternals naming and UX conventions** for output, exit codes, and flag style. When in doubt, mimic `psexec` / `procmon` / `accesschk`.
11. **Compatibility shim for `xpctl` users.** A one-time migration command: `xpc migrate-from-xpctl` that reads `~/.xpcli/config` and produces `~/.xpc/config` + `~/.xpc/credentials`.

---

## 12. Testing Strategy

Three layers, all required:

1. **Unit tests** — pure-function tests on protocol encoders/decoders, arg parsers, output formatters. Property-based where it makes sense (especially the wire protocol).
2. **Integration tests with a mock agent** — host CLI talks to an in-process or local-TCP mock agent. CI runs these on every PR. Fast, deterministic.
3. **Real-VM tests** — full end-to-end against the XP VM. Run manually before merging significant phases; run on a manually-triggered CI workflow before any release tag. The VM connection details come from `/Users/nficano/.xpcli/config` (or its `~/.xpc/` equivalent after migration).

**MCP smoke tests** for any subcommand that benefits from LLM integration (none initially, but `xpc dbg` may use an LLM later for debugger transcript analysis): use the keys in `~/.profile.local`. Source the file before running:
```
source ~/.profile.local && go test -tags=mcp ./...
```
Never commit these keys. Add `.profile.local` to a global gitignore note in the README.

---

## 13. Sub-agent Dispatch Patterns

Use sub-agents when:
- A subcommand has zero shared state with other in-flight work
- The shared infrastructure (protocol, daemon, profile system) is stable
- A clear acceptance criterion can be specified in advance

Do not use sub-agents for:
- Architectural exploration (sequential thinking required)
- Bug investigations (state and context matter)
- Anything during phases 0–5

Pattern when dispatching:
1. Coordinator agent updates `TASKS.md` to mark the subcommand as `[~] dispatched to sub-agent <id>`.
2. Sub-agent operates on a feature branch named `subcommand/<name>`.
3. Sub-agent's exit deliverable is a green PR with all tests, real-VM session log, and `TASKS.md` updates.
4. Coordinator merges and unblocks dependent work.

---

## 14. Hard Gate Summary

| Gate | Pass condition |
|---|---|
| End of Phase 0 | `INVESTIGATION.md` complete, all open questions answered by user |
| End of Phase 1 | User signs off on `docs/ARCHITECTURE.md` |
| End of Phase 2 | Private GH repo exists, CI green on scaffold, `TASKS.md` populated, branch protection on |
| End of Phase 3 | Real-network protocol round-trip passes against the XP VM |
| End of Phase 4 | Agent installable as service, test client gets `dir C:\` output back |
| End of Phase 5 | `xpc exec dir C:\` end-to-end against real VM, completion works in bash+zsh |
| End of each subcommand | Unit + integration + real-VM tests green, `TASKS.md` and `CHANGELOG.md` updated, PR merged |

---

## 15. First Action

Right now, before doing anything else:

1. Acknowledge this prompt by reading it through and producing a one-paragraph summary of your understanding of the mission.
2. Begin Phase 0: clone `~/code/xpctl`, read `~/.xpcli/config`, fetch both gists, and start populating `INVESTIGATION.md` (locally, since the repo doesn't exist yet — use a scratch directory).
3. Surface open questions to the user as you find them, in batches when convenient. Do not block on small questions you can answer yourself; do block on architectural ones.
4. When Phase 0 is complete, present the architecture-decision document and wait for sign-off.

Do not write production code until Phase 1 has cleared the gate.
