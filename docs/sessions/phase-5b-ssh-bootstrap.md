# Phase 5b — SSH-driven bootstrap + agent lifecycle

**Date:** 2026-05-09
**Branch:** `phase-5/host-cli` (continuing local; cumulative push pending)

---

## What landed

`xpc bootstrap` is no longer "print manual steps." End-to-end on a single
command: generate cert + PSK locally, SSH to the VM, upload all six files,
restart the agent, wait for the listener, pin fingerprint + PSK into the
profile.

* `internal/sshlife` — minimal SSH client wrapping `golang.org/x/crypto/ssh`:
  `Dial(addr, DialOptions)` with password auth and TOFU-on-deferred host-key
  verification; `Run(cmd, timeout)` returns stdout/stderr/exitStatus;
  `PutFile(localPath, remotePath, timeout)` and `PutBytes(...)` upload via
  the Cygwin `cat > <remote>` stdin-pipe technique. Win32 paths
  (`C:\xpc\foo`) are auto-translated to `/cygdrive/c/xpc/foo` for the bash
  invocation.

* `agent/embed.go` — `//go:embed agent.py arcp.py` so the Go binary ships
  the on-VM sources, plus a `ManagePy` constant carrying the lifecycle
  helper (kill / start / restart with `os.dup2` to NUL for proper
  detachment).

* `internal/cli/bootstrap.go` — replaces the Phase 5 stub. Generates fresh
  RSA-2048 cert + 32-byte PSK at `~/.xpc/material/<profile>/`, SSHes to the
  VM, uploads `agent.py`, `arcp.py`, `manage.py`, cert, key, PSK, restarts
  via `python.exe 'C:\xpc\manage.py' restart <port>`, polls until the
  agent's TCP listener is up, saves fingerprint + PSK into the profile.
  `--no-deploy` keeps the legacy "print manual steps" mode.

* `internal/cli/agent_lifecycle.go` — `xpc agent {start, stop, restart, tail}`
  drive `manage.py` over SSH. `start`/`restart` poll `waitForListen` so
  callers can chain `agent start; agent ping` with no sleep. `agent tail`
  cats `C:\xpc\agent.runlog`.

## Real-VM run

```text
$ ./bin/xpc bootstrap
Generated bootstrap material under /Users/nficano/.xpc/material/lab
  cert:        .../agent.crt
  key:         .../agent.key.pem
  psk (hex):   .../agent.key
  fingerprint: 35f04ec8891058a515b66c484679cce50519aebfa3c3eccb34d7f19483c2ced3

Connecting via SSH to DONALD TRUMP@xp-truvoice-w02 ...
Uploading agent files to C:\xpc\ ...
  agent.py -> C:\xpc\agent.py
  arcp.py -> C:\xpc\arcp.py
  manage.py -> C:\xpc\manage.py
  agent.crt -> C:\xpc\agent.crt
  agent.key.pem -> C:\xpc\agent.key.pem
  agent.key -> C:\xpc\agent.key
Restarting xpc agent ...
Waiting for the agent on xp-truvoice-w02:9579 ...
Pinning fingerprint and saving credentials to profile "lab" ...

Bootstrap complete. Try:
  xpc use lab
  xpc agent ping
  xpc exec ver

$ ./bin/xpc agent ping
pong from xp-truvoice-w02 in 5.51275ms

$ ./bin/xpc agent info
agent:    xpc v0.1.0
python:   3.4.10
pid:      1836
uptime:   10s

$ ./bin/xpc exec -- ver
Microsoft Windows XP [Version 5.1.2600]

$ ./bin/xpc agent stop && ./bin/xpc agent start && ./bin/xpc agent ping
agent stopped on xp-truvoice-w02:9579
agent restarted on xp-truvoice-w02:9579
pong from xp-truvoice-w02 in 4.634833ms
```

## Quoting wart that bit me twice

Cygwin bash strips backslashes from unquoted args, but Win32 `python.exe`
requires Win32 paths in `argv[1]`. The fix on the host side is:

```text
/cygdrive/c/Python34/python.exe 'C:\xpc\manage.py' restart 9579
```

POSIX path for the binary so bash's `$PATH` lookup finds it; single-quoted
Win32 path for the script argument so bash hands the bytes to `python.exe`
unchanged.

## Phase 5b exit gate: PASSED

- [x] `xpc bootstrap` deploys end-to-end over SSH (no manual steps).
- [x] `xpc agent {start,stop,restart,tail}` work over SSH against the VM.
- [x] Profile auto-pins fingerprint + PSK after a successful deploy.
- [x] `agent start` waits for the TCP listener so chained calls don't race.
- [x] All Go tests green; `golangci-lint` clean (0 issues).
- [x] Real-VM verified (this file).

## Still deferred

- `xpc daemon` host-side multiplex (Phase 5b extension).
- TOFU host-key verification for SSH (currently `InsecureIgnoreHostKey()`).
