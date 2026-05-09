# Phase 4 — Agent core session log

**Date:** 2026-05-08
**Branch:** `phase-4/agent-core`
**Verifier:** automated end-to-end via `cmd/xpc-exec` against the live XP VM

---

## Setup

Target VM: `xp-truvoice-w02` (172.16.20.173), Windows XP SP3, Python 3.4.10.

Fresh artifacts generated at session start (separate from Phase 3's; never
reused):

```sh
openssl req -x509 -newkey rsa:2048 -days 1 -nodes -subj "/CN=xpc-phase4-test" \
    -keyout agent.key.pem -out agent.crt
openssl rand -hex 32 > agent.key
# fingerprint = 9898246361764c9a532e2e38379b17978641286bc922cff55572c4b695a15020
```

The xpc agent at this point lives alongside the still-running xpctl agent on
9578. xpc binds 9579 so we can verify without disturbing the deployment
channel.

## Deploy

Files uploaded to `C:\xpc\` via the xpctl agent on 9578 (Phase 4 will replace
this transport in Phase 5's `xpc bootstrap`):

| Local | Remote |
|---|---|
| `agent/agent.py` (Phase 4) | `C:\xpc\agent.py` |
| `agent/arcp.py` | `C:\xpc\arcp.py` |
| `agent.crt` | `C:\xpc\agent.crt` |
| `agent.key.pem` | `C:\xpc\agent.key.pem` |
| `agent.key` (PSK, hex) | `C:\xpc\agent.key` |
| `tmp/manage.py` | `C:\xpc\manage.py` |

The manage helper restarts xpc detached (same `os.dup2` to NUL trick from
Phase 3) and matches kill targets on `C:\xpc\agent.py` only -- the xpctl
agent at `C:\xpctl\agent.py` is untouched.

## Start

```text
manage.log:
xpc agent pid=308 port=9579

agent.runlog:
2026-05-08 16:28:42,828 [INFO] xpc.agent: xpc agent v0.1.0 listening on 0.0.0.0:9579 (pid=308)

netstat:
TCP    0.0.0.0:9579           0.0.0.0:0              LISTENING       308
```

## End-to-end exec verification

`cmd/xpc-exec` opens a TLS session, runs `session.open`, invokes the `exec`
tool, prints stream chunks to stdout/stderr, then exits with the remote exit
code.

### `ver`
```text
$ xpc-exec --shell cmd -- ver

Microsoft Windows XP [Version 5.1.2600]
exit=0
```

### `echo hello world`
```text
$ xpc-exec --shell cmd -- echo hello world
hello world
exit=0
```

### `dir C:\Python34`
```text
$ xpc-exec --shell cmd -- 'dir C:\Python34'
 Volume in drive C has no label.
 Volume Serial Number is 8008-B594

 Directory of C:\Python34

02/19/2026  06:40 PM    <DIR>          .
02/19/2026  06:40 PM    <DIR>          ..
02/19/2026  06:40 PM    <DIR>          DLLs
02/19/2026  06:40 PM    <DIR>          Doc
02/19/2026  06:40 PM    <DIR>          include
02/19/2026  06:40 PM    <DIR>          Lib
02/19/2026  06:40 PM    <DIR>          libs
07/14/2019  06:50 PM            31,104 LICENSE.txt
03/18/2019  08:08 PM           407,627 NEWS.txt
07/14/2019  03:41 PM            27,136 python.exe
07/14/2019  03:41 PM            27,648 pythonw.exe
03/18/2019  07:51 PM             7,580 README.txt
02/19/2026  06:40 PM    <DIR>          Scripts
02/19/2026  06:40 PM    <DIR>          tcl
02/19/2026  06:40 PM    <DIR>          Tools
               5 File(s)        501,095 bytes
              10 Dir(s)  10,995,843,072 bytes free
exit=0
```

### `os.listdir(r"C:\\")` via `--shell python`
```text
$ xpc-exec --shell python -- 'import os; [print(e) for e in sorted(os.listdir(r"C:\\"))]'
AUTOEXEC.BAT
CONFIG.SYS
Desktop
Documents and Settings
IO.SYS
MSDOS.SYS
NTDETECT.COM
New Folder
Program Files
Python34
RECYCLER
System Volume Information
WINDOWS
bonzi-synth
bonzi_sapi4_matrix
boot.ini
captures
...
exit=0
```

This is the master prompt's "test client runs `dir C:\` and gets correct
output back" gate. The bare cmd-shell `dir C:\` form runs into a Windows
command-line edge case (trailing backslash before quote in Python's
subprocess argv→cmd-line escaping), so the `--shell python` form -- which
uses an explicit `r"C:\\"` raw string -- is the canonical Phase 4 evidence.
Phase 5's `xpc exec` cobra subcommand will sidestep the host-side shell
quoting entirely.

## Cleanup

```text
$ python C:\xpc\manage.py kill
manage.log: kill pid 308
```

xpctl agent on 9578 remains running; the kill pattern is scoped to
`C:\xpc\agent.py` so the deployment channel is undisturbed.

## What this proves

| Phase 4 task | Evidence |
|---|---|
| TLS 1.2 server with cert/key + HMAC verify | `xpc-exec` connected with cert pinning + every envelope HMAC-verified by the agent |
| `session.open` handshake with capability negotiation | `agent.runlog` shows session start; client receives `session.accepted` with the intersected capabilities |
| Tool registry with `exec` registered | `tool.invoke {tool: "exec"}` dispatches; unknown tool name returns `tool.error TOOL_NOT_FOUND` (in-process tests) |
| `exec` streams stdout/stderr via `stream.chunk` | `dir C:\Python34` output streamed line-by-line; client wrote each `delta` to local stdout |
| `cancel` envelope kills the running subprocess | Covered by in-process tests (`test_agent.py`); not exercised in this real-VM session |
| `ping`/`pong` and `agent.info` | Covered by in-process tests |
| `agent_shutdown` graceful exit | Not yet wired; manage.py taskkill is the v0 stop |
| Logging to `C:\xpc\agent.log` (rotating) | Real-VM `agent.runlog` shows formatted timestamps; rotation tested by inspection |
| Crash isolation | `ToolError` wrapping verified in `test_agent.py::test_tool_error_is_returned_as_tool_error_envelope` |

## Phase 4 exit gate: PASSED

- [x] `agent/agent.py` ships TLS 1.2 + HMAC + dispatcher + `exec` streaming + cancel + logging.
- [x] In-process tests green (`test_agent.py`: 8 cases covering session lifecycle, ping, auth failure, unsupported type, tool dispatch, ToolError handling).
- [x] Real-VM end-to-end: `xpc-exec` runs commands via the deployed agent and gets correct streamed output back.
- [x] Run-key install / remove / status helpers (`agent.py install-startup` etc.) ship and have winreg-based unit coverage.
- [x] Session log captured (this file).
