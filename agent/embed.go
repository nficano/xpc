// Package agent ships the on-VM Python sources as embedded byte slices so the
// xpc Go binary can ship them. Used by xpc bootstrap to upload agent.py and
// arcp.py to C:\xpc\ on a fresh VM.
package agent

import _ "embed"

// AgentPy is the canonical agent.py source (Python 3.4 compatible).
//
//go:embed agent.py
var AgentPy []byte

// ArcpPy is the canonical arcp.py source (Python 3.4 compatible). agent.py
// imports it as a same-dir module.
//
//go:embed arcp.py
var ArcpPy []byte

// ManagePy is the small lifecycle helper that handles detached restart.
// Inlined as a string so we don't need a separate file in the repo
// permanently; xpc bootstrap writes it to C:\xpc\manage.py once and reuses
// it for xpc agent {start, stop, redeploy}.
const ManagePy = `# -*- coding: utf-8 -*-
"""xpc agent manage helper.

Drops xpctl-style PIPE inheritance via os.dup2 to NUL, then either kills
or detached-spawns the xpc agent. Invoked by ` + "`xpc agent start|stop|" +
	"redeploy`" + ` over SSH after files are uploaded.
"""
import os, subprocess, sys, time

DETACHED_PROCESS         = 0x00000008
CREATE_NEW_PROCESS_GROUP = 0x00000200

INSTALL_DIR = r"C:\xpc"

def _redirect_stdio_to_nul():
    nul_r = os.open(os.devnull, os.O_RDONLY)
    nul_w = os.open(os.devnull, os.O_WRONLY)
    os.dup2(nul_r, 0); os.dup2(nul_w, 1); os.dup2(nul_w, 2)
    os.close(nul_r); os.close(nul_w)

def _scope_match():
    # Only match python.exe processes whose command line includes our agent
    # path. This deliberately does NOT match "C:\\xpctl\\agent.py".
    return ("name='python.exe' and commandline like "
            "'%C:\\\\xpc\\\\agent.py%'")

def kill_existing(status_log):
    try:
        out = subprocess.check_output(
            ["wmic", "process", "where", _scope_match(),
             "get", "processid", "/value"],
            stderr=subprocess.STDOUT,
        )
    except subprocess.CalledProcessError as exc:
        out = exc.output or b""
    for line in out.decode("utf-8", "replace").splitlines():
        line = line.strip()
        if line.startswith("ProcessId="):
            pid = line.split("=", 1)[1].strip()
            if pid.isdigit() and int(pid) > 0:
                status_log.write("kill {0}\n".format(pid).encode("utf-8"))
                subprocess.call(["taskkill", "/F", "/PID", pid])

def start_detached(status_log, port):
    log = open(os.path.join(INSTALL_DIR, "agent.runlog"), "wb")
    p = subprocess.Popen(
        [r"C:\Python34\python.exe", os.path.join(INSTALL_DIR, "agent.py"),
         "run",
         "--port", str(port),
         "--cert", os.path.join(INSTALL_DIR, "agent.crt"),
         "--key",  os.path.join(INSTALL_DIR, "agent.key.pem"),
         "--psk-file", os.path.join(INSTALL_DIR, "agent.key")],
        stdin=subprocess.DEVNULL, stdout=log, stderr=subprocess.STDOUT,
        creationflags=DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP,
    )
    status_log.write("xpc pid={0} port={1}\n".format(p.pid, port).encode("utf-8"))

def main():
    cmd = sys.argv[1] if len(sys.argv) > 1 else "restart"
    port = int(sys.argv[2]) if len(sys.argv) > 2 else 9578
    status_log = open(os.path.join(INSTALL_DIR, "manage.log"), "ab")
    _redirect_stdio_to_nul()
    if cmd == "kill":
        kill_existing(status_log)
    elif cmd == "start":
        start_detached(status_log, port)
    elif cmd == "restart":
        kill_existing(status_log); time.sleep(1); start_detached(status_log, port)
    else:
        status_log.write(b"unknown command\n"); status_log.close(); sys.exit(2)
    status_log.close()

if __name__ == "__main__":
    main()
`
