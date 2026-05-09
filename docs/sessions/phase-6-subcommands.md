# Phase 6 — Subcommand surface (first wave)

**Date:** 2026-05-08
**Branch:** `phase-5/host-cli` (continuing; Phase 6 PR will branch from main once Phase 5 merges)

---

## What landed in this wave

Sysinternals-flat top-level commands plus a few groups, all driven by the
same `internal/cli/session.go` session-open + tool.invoke flow, with most
agent-side work avoided by routing through `--shell python` + `subprocess`
(argv form, no cmd.exe quoting).

### Diagnostics (read-only)
* `xpc info`           -- `systeminfo`
* `xpc ps [--filter]`  -- structured `tasklist /v /fo csv` parser
* `xpc net`, `xpc net ipconfig | netstat | route`

### Registry
* `xpc reg get <key> [--value] [--recurse]`
* `xpc reg set <key> <name> <data> [--type] [--force]`
* `xpc reg delete <key> [--value] [--force]`
* `xpc reg export <key> <vm-path>`

  All four route via `python -c "subprocess.Popen(['reg', 'query', ...])"`
  to bypass cmd.exe argv-escaping bugs (registry paths often contain spaces
  + backslashes, e.g. `Windows NT`).

### Services / env / batch / events
* `xpc svc list | start | stop | status` (idempotent already-running /
  already-stopped detection)
* `xpc env list | set` (`setx` for persistence)
* `xpc bat run <vm-path> [args...]`
* `xpc evt query [--log] [--max] [--type]` -- wraps XP's `eventquery.vbs`

### Loop / retry
* `xpc watch -- <cmd>` -- repeats a remote command at `--interval`
  (replaces `xpctl watch`).

### Python on the VM
* `xpc py run -- <source>`           -- one-shot `python -c`
* `xpc py local <local.py>`          -- ships a local file as `python -c`
                                        source (replaces `xpctl script`)
* `xpc py pip [args...]`             -- `python -m pip`

### Files
* `xpc cp <src> <dst>`               -- bidirectional copy with `host:`/`vm:`
                                        prefixes; v0 inline (~30 MB cap)
* `xpc cat <vm:path>`                -- python-shell-driven; backslash-safe
* `xpc head -n N <vm:path>`
* `xpc tail -n N <vm:path>`
* `xpc find <vm:path> [--glob] [--regex]`
* `xpc sum <vm:path> [--algo]`       -- md5 / sha1 / sha256

### Reverse-engineering
* `xpc dll list <pid>`               -- `tasklist /m /fi "PID eq ..."`
* `xpc dll regsvr32 <vm:path-to-dll> [--unregister]`
* `xpc shot [-o <host-bmp>]`         -- BitBlt + GetDIBits ctypes capture,
                                        24-bit BMP, base64 transfer back

## Real-VM verifications (against `xp-truvoice-w02:9579`)

```text
$ ./bin/xpc info | head -3
Host Name:                 XP-TRUVOICE-W02
OS Name:                   Microsoft Windows XP Professional
OS Version:                5.1.2600 Service Pack 3 Build 2600

$ ./bin/xpc ps --filter python
PID     NAME                               MEMORY USER
1200    python.exe                          9288K XP-TRUVOICE-W02\DONALD TRUMP
1864    python.exe                         11240K XP-TRUVOICE-W02\DONALD TRUMP

$ ./bin/xpc reg get 'HKLM\Software\Microsoft\Windows NT\CurrentVersion' --value ProductName
HKEY_LOCAL_MACHINE\Software\Microsoft\Windows NT\CurrentVersion
    ProductName  REG_SZ  Microsoft Windows XP

$ ./bin/xpc reg get 'HKLM\Software\Microsoft\Windows NT\CurrentVersion' --value CSDVersion
HKEY_LOCAL_MACHINE\Software\Microsoft\Windows NT\CurrentVersion
    CSDVersion  REG_SZ  Service Pack 3

$ ./bin/xpc cp /tmp/xpc-cp-source.txt 'C:\xpc\cp-test.txt'
wrote 34 bytes -> C:\xpc\cp-test.txt

$ ./bin/xpc cp 'C:\xpc\cp-test.txt' host:/tmp/xpc-cp-roundtrip.txt
wrote 34 bytes -> /tmp/xpc-cp-roundtrip.txt

$ ./bin/xpc cat 'C:\xpc\cp-test.txt'
hello from xpc cp test 2026-05-08

$ ./bin/xpc head -n 5 'C:\boot.ini'
[boot loader]
timeout=30
default=multi(0)disk(0)rdisk(0)partition(1)\WINDOWS
[operating systems]
multi(0)disk(0)rdisk(0)partition(1)\WINDOWS="Microsoft Windows XP Professional" /noexecute=optin /fastdetect

$ ./bin/xpc sum 'C:\boot.ini'
69c6eaa43ec6b89a61e0c6294be8ea88447efa011b3d266de9213e45336d6118  C:\boot.ini

$ ./bin/xpc shot -o /tmp/xpc-shot.bmp
wrote 3686454 bytes -> /tmp/xpc-shot.bmp
$ file /tmp/xpc-shot.bmp
PC bitmap, Windows 3.x format, 1280 x 960 x 24, image size 3686400
```

## Deferred to follow-up sessions

Per MASTER.md §10 ordering, still needed:

| # | Subcommand | Why deferred |
|---|---|---|
| 6  | `xpc send keys/click/move` | Needs ctypes SendInput on the VM; mid-effort |
| 8  | `xpc tun -L|-R`            | ARCP stream multiplexing for TCP forwards; non-trivial |
| 10 | `xpc dump <pid>`           | MiniDumpWriteDump via dbghelp.dll ctypes |
| 10 | `xpc inj <pid> <dll>`      | CreateRemoteThread + LoadLibraryA via ctypes |
| 11 | `xpc boot {shutdown,pause,resume}` | reboot exists; pause/resume need Proxmox API |
| 11 | `xpc snap {list,create,restore,delete}` | Proxmox host + auth still pending in TASKS open questions |
| 12 | `xpc dbg attach/run/server` | Long-running debugger sessions; persistent state |
| 13 | `xpc trace start/stop/pull` | procmon wrapper; needs file pull + parsing |
| 14 | `xpc ghidra start/stop`     | ghidra_server lifecycle + tunnel |
| 14 | `xpc ida start/stop`        | IDA remote-debug stub + tunnel |
|   | `xpc fetch <url>`           | URL → VM via existing cp pattern; small follow-up |
|   | `xpc edit <vm:path>`        | cp pull → $EDITOR → cp push wrapper |
|   | `xpc agent {start,stop,...}` | Needs `internal/sshlife/` Go SSH package |
|   | `xpc daemon`                 | Phase 5b host-side multiplex |

## Phase 6 (first wave) exit gate: PASSED for landed commands

- [x] All Go tests green; `golangci-lint` clean.
- [x] All Python tests green (50 + 2 skipped corpus indices).
- [x] Each landed subcommand verified against the live xpc agent.
- [x] Session log captured (this file).
- [x] Local commit recorded.
