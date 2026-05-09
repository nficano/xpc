# Phase 5 — Host CLI core session log

**Date:** 2026-05-08
**Branch:** `phase-5/host-cli`

---

## Setup

The Phase 4 xpc agent is already deployed at `C:\xpc\` on
`xp-truvoice-w02`. Restarted on port 9579 (xpctl stays on 9578 as the
deployment channel) for this session.

```text
manage.log: xpc agent pid=1864 port=9579
netstat:    TCP 0.0.0.0:9579  LISTENING  1864
```

## Build

```sh
$ make build
go build -o bin/xpc ./cmd/xpc
$ ls -l bin/xpc
-rwxr-xr-x  1 nficano  staff  10203506  bin/xpc
```

## CLI surface

```text
$ ./bin/xpc --help
Modern remote management toolkit for Windows XP VMs.

Usage:
  xpc [command]

Available Commands:
  agent              Lifecycle and diagnostics for the on-VM xpc agent.
  bootstrap          Generate cert + key + PSK locally and emit manual deploy steps.
  completion         Generate shell completion script for bash, zsh, fish, or powershell.
  configure          Interactively set up a profile in ~/.xpc/config + ~/.xpc/credentials.
  exec               Run a command on the remote XP VM and stream its output.
  help               Help about any command
  migrate-from-xpctl Read ~/.xpcli/config and write equivalent ~/.xpc/{config,credentials} entries.
  profile            Manage saved connection profiles (~/.xpc/config + ~/.xpc/credentials).
  use                Alias for `xpc profile use <name>`.
  version            Print the xpc client version.
```

Global flags: `--profile`, `--host`, `--port`, `--output`, `-v/--verbose`,
`--timeout`, `--dry-run`. Each subcommand resolves the profile lazily through
`Globals.ResolveProfile()`, which merges file → env → CLI flags.

## Profile + bootstrap-import flow

```text
$ ./bin/xpc profile add lab \
    --host xp-truvoice-w02 \
    --port 9579 \
    --fingerprint 9898246361764c9a532e2e38379b17978641286bc922cff55572c4b695a15020 \
    --psk-file /tmp/agent.key
saved profile "lab"

$ ./bin/xpc profile list
  lab

$ ./bin/xpc use lab
active profile -> "lab"
```

`~/.xpc/config` (mode 0600):
```ini
[profile lab]
host            = xp-truvoice-w02
port            = 9579
fingerprint     = 9898246361764c9a532e2e38379b17978641286bc922cff55572c4b695a15020
ssh_user        =
verify_host_key = true
```

`~/.xpc/credentials` (mode 0600) carries the base64-encoded PSK.
`~/.xpc/state` (mode 0600) is a single line: `lab`.

## Real-VM verifications

### `xpc agent ping`
```text
pong from xp-truvoice-w02 in 4.412875ms
```

### `xpc agent info`
```text
agent:    xpc v0.1.0
python:   3.4.10
pid:      1864
uptime:   16s
```

### `xpc exec ver` (Phase 5 exit gate, simple form)
```text
$ ./bin/xpc exec -- ver
Microsoft Windows XP [Version 5.1.2600]
exit=0
```

### `xpc exec 'dir C:\Python34'` (Phase 5 exit gate, full streaming)
```text
$ ./bin/xpc exec -- 'dir C:\Python34'
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
              10 Dir(s)  10,995,281,920 bytes free
```

### `xpc exec --shell python`
```text
$ ./bin/xpc exec --shell python -- 'import sys; print("py", sys.version.split()[0])'
py 3.4.10
```

### Shell completion
```text
$ ./bin/xpc completion bash | head
# bash completion V2 for xpc                                  -*- shell-script -*-

__xpc_debug()
{
    if [[ -n ${BASH_COMP_DEBUG_FILE-} ]]; then
        echo "$*" >> "${BASH_COMP_DEBUG_FILE}"
    fi
}

$ ./bin/xpc completion zsh | head
#compdef xpc
compdef _xpc xpc

# zsh completion for xpc                                  -*- shell-script -*-
```

Both bash and zsh completion scripts produced by cobra; install per shell:

* bash: `source <(./bin/xpc completion bash)`
* zsh:  add `./bin/xpc completion zsh > "${fpath[1]}/_xpc"` to your zshrc

### Exit codes
```text
xpc exec               -> 1   (cobra arg validation; generic error)
xpc --profile zzz exec -> 2   (UsageError: missing host/PSK in profile)
xpc exec -- false      -> 1   (RemoteError: cmd.exe returned 1)
```

UsageError → 2, ConnectionError → 3, AuthError → 4, RemoteError → propagates
remote exit code (or 5). Cobra arg validation falls through to generic 1 for v0.

## migrate-from-xpctl

Synthetic xpctl config:
```ini
[lab]
hostname = xp-truvoice-w02
port = 9578
transport = auto
username = DONALD TRUMP
password = mywinxp!
```

```text
$ HOME=/tmp/xpc-mig-test ./bin/xpc migrate-from-xpctl
migrated lab -> ~/.xpc/{config,credentials}
```

The migrator copies host/port/username/password fields. Fingerprint and PSK
must come from `xpc bootstrap` (or the manual flow) since xpctl never had
those.

## Phase 5 exit gate: PASSED

- [x] Cobra command tree implemented: configure, profile {list,add,remove,use},
      use, completion, migrate-from-xpctl, exec, bootstrap, agent {ping,info},
      version.
- [x] AWS-style profile split (~/.xpc/config + ~/.xpc/credentials + ~/.xpc/state)
      with file/env/flag merge.
- [x] Real-VM `xpc exec dir 'C:\Python34'` streams correctly via the cobra
      subcommand.
- [x] Bash and zsh completion scripts generated by cobra and known-good.
- [x] `xpc configure` interactive flow works.
- [x] `xpc migrate-from-xpctl` reads ~/.xpcli/config and writes ~/.xpc/.
- [x] Session log captured (this file).

### Deferred to Phase 5b / 6

- SSH-driven `xpc bootstrap` (currently emits manual instructions and
  generates trust material; Phase 5b adds golang.org/x/crypto/ssh-driven
  deploy).
- `xpc daemon` host-side multiplex (Phase 5b).
- `xpc agent {start, stop, redeploy, install, uninstall, startup-status,
  reboot}` lifecycle commands (Phase 5b — needs SSH).
- Cobra arg-validation errors mapping to exit 2 (currently fall through to 1).
