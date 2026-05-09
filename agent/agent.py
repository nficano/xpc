# -*- coding: utf-8 -*-
"""xpc serve -- the on-VM agent for Windows XP.

Single-file Python 3.4 agent. Listens on TCP with TLS 1.2, verifies HMAC on
every envelope, dispatches tool.invoke to registered tools. v0 ships with one
tool (exec) that streams stdout/stderr back via stream.chunk envelopes and
finishes with tool.result/job.completed.

Compatibility note: this file targets the Python 3.4.10 build that lives on
the XP VM. Avoid f-strings, walrus, and type annotations in signatures. The
agent is stdlib-only (ssl, socket, subprocess, threading, ctypes, winreg).

Lifecycle (typical):

    python agent.py run --cert C:\\xpc\\agent.crt --key C:\\xpc\\agent.key.pem \\
                         --psk-file C:\\xpc\\agent.key

The bootstrap flow on the host generates the cert + PSK, uploads everything,
then either starts the agent ad-hoc or runs ``install-startup`` to register a
HKLM Run key so it auto-starts at next login.
"""
from __future__ import absolute_import, print_function

import argparse
import base64
import binascii
import logging
import logging.handlers
import os
import socket
import ssl
import subprocess
import sys
import threading
import time

# Same-dir import of arcp (xpc agent ships arcp.py alongside agent.py).
HERE = os.path.dirname(os.path.abspath(__file__))
if HERE not in sys.path:
    sys.path.insert(0, HERE)

import arcp  # noqa: E402

AGENT_VERSION = "0.1.0"
DEFAULT_PORT = 9578
DEFAULT_BIND = "0.0.0.0"
DEFAULT_INSTALL_DIR = r"C:\xpc"
DEFAULT_LOG_PATH = r"C:\xpc\agent.log"
LOG_MAX_BYTES = 1 * 1024 * 1024
LOG_BACKUP_COUNT = 3
PYTHON_EXE = r"C:\Python34\python.exe"

log = logging.getLogger("xpc.agent")


# ---- Outbound write lock ---------------------------------------------------

class ConnectionWriter(object):
    """Serializes signed-envelope writes to a TLS stream from many threads."""

    def __init__(self, wfile, psk):
        self._wfile = wfile
        self._psk = psk
        self._lock = threading.Lock()
        self._closed = False

    def send(self, envelope):
        if self._closed:
            return
        arcp.sign(envelope, self._psk)
        with self._lock:
            if self._closed:
                return
            try:
                arcp.write_frame(self._wfile, envelope)
                self._wfile.flush()
            except (OSError, ssl.SSLError, ValueError) as exc:
                log.debug("writer dropped: %s", exc)
                self._closed = True

    def close(self):
        self._closed = True


# ---- Tool API --------------------------------------------------------------

class ToolError(Exception):
    """Raised by a tool to send a structured tool.error envelope."""

    def __init__(self, code, message, retryable=False):
        super(ToolError, self).__init__(message)
        self.code = code
        self.message = message
        self.retryable = retryable


class ToolContext(object):
    """Per-invocation context handed to tool handlers."""

    def __init__(self, session_id, job_id, msg_id, trace_id, writer, cancel_event):
        self.session_id = session_id
        self.job_id = job_id
        self.msg_id = msg_id
        self.trace_id = trace_id
        self.writer = writer
        self.cancel = cancel_event

    def emit(self, msg_type, payload, **fields):
        """Emit an envelope sourced from this job's context.

        Optional fields like stream_id / correlation_id are added when
        non-empty. session_id, job_id, trace_id are always populated when
        the context has them.
        """
        env_id = arcp.new_id(arcp.PREFIX_MESSAGE)
        ts = arcp.format_timestamp()
        envelope = arcp.new_envelope(env_id, msg_type, ts)
        envelope["payload"] = payload
        if self.session_id:
            envelope["session_id"] = self.session_id
        if self.job_id:
            envelope["job_id"] = self.job_id
        if self.trace_id:
            envelope["trace_id"] = self.trace_id
        if self.msg_id:
            envelope["correlation_id"] = self.msg_id
        for k, v in fields.items():
            if v:
                envelope[k] = v
        self.writer.send(envelope)


class Job(object):
    """Bookkeeping for a single in-flight tool invocation."""

    def __init__(self, job_id, msg_id):
        self.job_id = job_id
        self.msg_id = msg_id
        self.cancel = threading.Event()
        self.proc = None  # set by tools that spawn subprocesses


# ---- Built-in tools --------------------------------------------------------

def tool_exec(arguments, ctx, job):
    """exec tool: spawn a subprocess, stream stdout+stderr, return exit code.

    Arguments:
      cmd     (str, required) -- command line
      shell   (str)           -- "cmd" (default), "python", or "python_file"
      timeout (number)        -- seconds; 0 or absent = no timeout
    """
    cmd = arguments.get("cmd", "")
    shell = arguments.get("shell", "cmd")
    timeout = arguments.get("timeout") or 0

    if not cmd:
        raise ToolError("INVALID_ARGS", "missing 'cmd'", retryable=False)

    if shell == "python":
        argv = [PYTHON_EXE, "-c", cmd]
    elif shell == "python_file":
        argv = [PYTHON_EXE, cmd]
    else:
        argv = ["cmd.exe", "/c", cmd]

    log.info("exec [%s][job=%s]: %.200s", shell, job.job_id, cmd)

    proc = subprocess.Popen(
        argv,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        bufsize=0,
    )
    job.proc = proc

    stdout_stream_id = arcp.new_id(arcp.PREFIX_STREAM)
    stderr_stream_id = arcp.new_id(arcp.PREFIX_STREAM)
    ctx.emit(arcp.TYPE_STREAM_OPEN,
             {"content_type": "text/plain", "channel": "stdout"},
             stream_id=stdout_stream_id)
    ctx.emit(arcp.TYPE_STREAM_OPEN,
             {"content_type": "text/plain", "channel": "stderr"},
             stream_id=stderr_stream_id)

    def _pump(stream, stream_id):
        try:
            while True:
                chunk = stream.read(8192)
                if not chunk:
                    return
                ctx.emit(arcp.TYPE_STREAM_CHUNK,
                         {"delta": chunk.decode("utf-8", "replace")},
                         stream_id=stream_id)
        except Exception:
            log.exception("stream pump %s error", stream_id)

    t_out = threading.Thread(target=_pump, args=(proc.stdout, stdout_stream_id))
    t_err = threading.Thread(target=_pump, args=(proc.stderr, stderr_stream_id))
    t_out.daemon = True
    t_err.daemon = True
    t_out.start()
    t_err.start()

    deadline = (time.time() + timeout) if timeout else None
    timed_out = False
    while True:
        if job.cancel.is_set():
            try:
                proc.kill()
            except Exception:
                pass
            break
        rc = proc.poll()
        if rc is not None:
            break
        if deadline is not None and time.time() >= deadline:
            timed_out = True
            try:
                proc.kill()
            except Exception:
                pass
            break
        time.sleep(0.05)

    proc.wait()
    t_out.join(timeout=2)
    t_err.join(timeout=2)

    ctx.emit(arcp.TYPE_STREAM_CLOSE, {"reason": "complete"}, stream_id=stdout_stream_id)
    ctx.emit(arcp.TYPE_STREAM_CLOSE, {"reason": "complete"}, stream_id=stderr_stream_id)

    if job.cancel.is_set():
        # Tool returns None to indicate it sent its own terminal envelopes.
        ctx.emit(arcp.TYPE_JOB_CANCELLED, {"state": "cancelled"})
        return None

    return {"exit_code": proc.returncode, "timed_out": timed_out}


def tool_agent_info(arguments, ctx, job):
    """Return agent metadata. Useful for sanity checks and version probes."""
    return {
        "agent": {
            "name": "xpc",
            "version": AGENT_VERSION,
            "python": sys.version.split()[0],
            "pid": os.getpid(),
        },
        "uptime_seconds": time.time() - SERVER_STARTED_AT,
    }


def tool_tun_connect(arguments, ctx, job):
    """tun.connect -- open a TCP socket on the VM and bidirectionally pump bytes.

    Arguments:
      host (str, required) -- target hostname or IP reachable from the VM
      port (int, required) -- target port

    Behavior:
      * Opens a TCP socket to (host, port).
      * Stores the socket on the job so the connection's stream.chunk handler
        (see Connection._handle_stream_chunk) can write client-sourced bytes
        into the VM-side socket.
      * Reads from the VM-side socket and emits stream.chunk envelopes
        (delta_b64) on a fresh upstream stream until EOF or cancel.
      * On exit, closes the VM-side socket and emits stream.close.
    """
    target_host = arguments.get("host", "")
    target_port = arguments.get("port", 0)
    if not target_host:
        raise ToolError("INVALID_ARGS", "host required", retryable=False)
    try:
        target_port = int(target_port)
    except (TypeError, ValueError):
        raise ToolError("INVALID_ARGS", "port must be an integer", retryable=False)
    if target_port <= 0 or target_port > 65535:
        raise ToolError("INVALID_ARGS", "port out of range", retryable=False)

    log.info("tun.connect [job=%s] -> %s:%d", job.job_id, target_host, target_port)

    try:
        sock = socket.create_connection((target_host, target_port), timeout=10)
    except (OSError, socket.error) as exc:
        raise ToolError("TUN_CONNECT_FAILED", str(exc), retryable=False)
    sock.settimeout(0.5)  # short timeout so we can poll job.cancel between reads

    # Expose the socket so Connection._handle_stream_chunk routes inbound
    # bytes to it.
    job.tun_socket = sock

    upstream_id = arcp.new_id(arcp.PREFIX_STREAM)
    ctx.emit(arcp.TYPE_STREAM_OPEN,
             {"content_type": "application/octet-stream", "channel": "upstream"},
             stream_id=upstream_id)

    closed_reason = "vm_eof"
    try:
        while not job.cancel.is_set():
            try:
                chunk = sock.recv(8192)
            except socket.timeout:
                continue
            except (OSError, socket.error) as exc:
                closed_reason = "vm_error: {0}".format(exc)
                break
            if not chunk:
                closed_reason = "vm_eof"
                break
            ctx.emit(arcp.TYPE_STREAM_CHUNK,
                     {"delta_b64": base64.b64encode(chunk).decode("ascii")},
                     stream_id=upstream_id)
    finally:
        try:
            sock.close()
        except Exception:
            pass
        try:
            del job.tun_socket
        except AttributeError:
            pass
        ctx.emit(arcp.TYPE_STREAM_CLOSE,
                 {"reason": closed_reason},
                 stream_id=upstream_id)

    return {"closed": True, "reason": closed_reason}


TOOLS = {
    "exec": tool_exec,
    "agent.info": tool_agent_info,
    "tun.connect": tool_tun_connect,
}


# ---- Connection handler ----------------------------------------------------

class Connection(object):
    """One client TLS connection. One read loop, multiple worker threads."""

    def __init__(self, sock, psk, server):
        self.sock = sock
        self.psk = psk
        self.server = server
        self.session_id = None
        self.rfile = sock.makefile("rb")
        self.wfile = sock.makefile("wb")
        self.writer = ConnectionWriter(self.wfile, psk)
        self.jobs = {}
        self.jobs_lock = threading.Lock()

    def serve(self):
        peer = "?"
        try:
            peer = self.sock.getpeername()
        except Exception:
            pass
        log.info("connection start: %s", peer)
        try:
            while True:
                envelope = arcp.read_frame(self.rfile)
                if envelope is None:
                    return
                try:
                    arcp.verify_sig(envelope, self.psk)
                except arcp.ProtocolError as exc:
                    log.warning("auth failed from %s: %s", peer, exc)
                    self._send_nack(envelope, "auth_failed", str(exc))
                    return
                self._dispatch(envelope)
        except (OSError, ssl.SSLError, ValueError) as exc:
            log.info("connection error from %s: %s", peer, exc)
        except Exception:
            log.exception("unexpected connection error from %s", peer)
        finally:
            self._teardown()
            log.info("connection end: %s", peer)

    def _teardown(self):
        with self.jobs_lock:
            jobs = list(self.jobs.values())
        for job in jobs:
            job.cancel.set()
            if job.proc is not None:
                try:
                    job.proc.kill()
                except Exception:
                    pass
        self.writer.close()
        for closer in (self.rfile, self.wfile, self.sock):
            try:
                closer.close()
            except Exception:
                pass

    def _dispatch(self, envelope):
        ty = envelope.get("type", "")
        try:
            if ty == arcp.TYPE_SESSION_OPEN:
                self._handle_session_open(envelope)
            elif ty == arcp.TYPE_SESSION_CLOSE:
                self._handle_session_close(envelope)
            elif ty == arcp.TYPE_PING:
                self._handle_ping(envelope)
            elif ty == arcp.TYPE_TOOL_INVOKE:
                self._handle_tool_invoke(envelope)
            elif ty == arcp.TYPE_CANCEL:
                self._handle_cancel(envelope)
            elif ty == arcp.TYPE_STREAM_CHUNK:
                self._handle_stream_chunk(envelope)
            elif ty == arcp.TYPE_STREAM_CLOSE:
                self._handle_stream_close(envelope)
            else:
                self._send_nack(envelope, "unsupported_type",
                                "type {0!r} not supported".format(ty))
        except Exception as exc:
            log.exception("dispatch error for type=%s", ty)
            self._send_nack(envelope, "invalid_envelope", str(exc))

    def _handle_stream_chunk(self, envelope):
        """Route a client-sourced stream.chunk to the matching tun socket.

        v0 only honors stream.chunk for jobs that own a tun_socket (i.e.
        tun.connect). For exec, stream.chunks flow agent->host only.
        """
        job_id = envelope.get("job_id", "")
        with self.jobs_lock:
            job = self.jobs.get(job_id)
        if job is None:
            return
        sock = getattr(job, "tun_socket", None)
        if sock is None:
            return
        delta_b64 = (envelope.get("payload") or {}).get("delta_b64", "")
        if not delta_b64:
            return
        try:
            sock.sendall(base64.b64decode(delta_b64))
        except (OSError, socket.error) as exc:
            log.debug("tun_socket sendall failed: %s", exc)
            try:
                sock.close()
            except Exception:
                pass

    def _handle_stream_close(self, envelope):
        """Client signalled end-of-input on its downstream stream; close the
        VM-side write half so the upstream pump in tool_tun_connect notices
        and exits."""
        job_id = envelope.get("job_id", "")
        with self.jobs_lock:
            job = self.jobs.get(job_id)
        if job is None:
            return
        sock = getattr(job, "tun_socket", None)
        if sock is None:
            return
        try:
            sock.shutdown(socket.SHUT_WR)
        except Exception:
            pass

    # ---- handlers ---------------------------------------------------------

    def _handle_session_open(self, envelope):
        if self.session_id is None:
            self.session_id = arcp.new_id(arcp.PREFIX_SESSION)
        client_caps = ((envelope.get("payload") or {}).get("capabilities") or {})
        # Server-side capabilities for v0.
        server_caps = {
            "streaming": True,
            "binary_streams": True,
            "durable_jobs": False,
            "checkpoints": False,
            "agent_handoff": False,
        }
        accepted = dict((k, bool(client_caps.get(k)) and v) for k, v in server_caps.items())
        resp = arcp.new_envelope(arcp.new_id(arcp.PREFIX_MESSAGE),
                                 arcp.TYPE_SESSION_ACCEPTED,
                                 arcp.format_timestamp())
        resp["session_id"] = self.session_id
        resp["correlation_id"] = envelope.get("id", "")
        if envelope.get("trace_id"):
            resp["trace_id"] = envelope["trace_id"]
        resp["payload"] = {
            "session_id": self.session_id,
            "agent": {"name": "xpc", "version": AGENT_VERSION,
                      "python": sys.version.split()[0]},
            "capabilities": accepted,
        }
        self.writer.send(resp)

    def _handle_session_close(self, envelope):
        try:
            self.sock.shutdown(socket.SHUT_RDWR)
        except Exception:
            pass

    def _handle_ping(self, envelope):
        resp = arcp.new_envelope(arcp.new_id(arcp.PREFIX_MESSAGE),
                                 arcp.TYPE_PONG,
                                 arcp.format_timestamp())
        resp["correlation_id"] = envelope.get("id", "")
        if self.session_id:
            resp["session_id"] = self.session_id
        if envelope.get("trace_id"):
            resp["trace_id"] = envelope["trace_id"]
        resp["payload"] = {"agent_version": AGENT_VERSION}
        self.writer.send(resp)

    def _handle_tool_invoke(self, envelope):
        if not self.session_id:
            self._send_nack(envelope, "invalid_envelope",
                            "tool.invoke before session.open")
            return
        payload = envelope.get("payload") or {}
        tool_name = payload.get("tool", "")
        handler = TOOLS.get(tool_name)
        if not handler:
            self._send_tool_error(envelope, "TOOL_NOT_FOUND",
                                  "tool {0!r} not registered".format(tool_name))
            return

        job_id = arcp.new_id(arcp.PREFIX_JOB)
        msg_id = envelope.get("id", "")
        trace_id = envelope.get("trace_id", "")

        job = Job(job_id, msg_id)
        with self.jobs_lock:
            self.jobs[job_id] = job

        # Acknowledge.
        accepted = arcp.new_envelope(arcp.new_id(arcp.PREFIX_MESSAGE),
                                     arcp.TYPE_JOB_ACCEPTED,
                                     arcp.format_timestamp())
        accepted["session_id"] = self.session_id
        accepted["job_id"] = job_id
        accepted["correlation_id"] = msg_id
        if trace_id:
            accepted["trace_id"] = trace_id
        accepted["payload"] = {"state": "accepted"}
        self.writer.send(accepted)

        ctx = ToolContext(self.session_id, job_id, msg_id, trace_id,
                          self.writer, job.cancel)
        thread = threading.Thread(
            target=self._run_job,
            args=(job, ctx, handler, payload.get("arguments") or {}, tool_name),
        )
        thread.daemon = True
        thread.start()

    def _run_job(self, job, ctx, handler, arguments, tool_name):
        ctx.emit(arcp.TYPE_JOB_STARTED, {"state": "running"})
        try:
            result = handler(arguments, ctx, job)
        except ToolError as exc:
            ctx.emit(arcp.TYPE_TOOL_ERROR, {
                "code": exc.code, "message": exc.message, "retryable": exc.retryable,
            })
            ctx.emit(arcp.TYPE_JOB_FAILED, {"state": "failed"})
        except Exception as exc:
            log.exception("tool %s crashed", tool_name)
            ctx.emit(arcp.TYPE_TOOL_ERROR, {
                "code": "INTERNAL", "message": str(exc), "retryable": False,
            })
            ctx.emit(arcp.TYPE_JOB_FAILED, {"state": "failed"})
        else:
            if result is not None:
                ctx.emit(arcp.TYPE_TOOL_RESULT, result)
                ctx.emit(arcp.TYPE_JOB_COMPLETED, {"state": "completed"})
        finally:
            with self.jobs_lock:
                self.jobs.pop(job.job_id, None)

    def _handle_cancel(self, envelope):
        target = ((envelope.get("payload") or {}).get("job_id")
                  or envelope.get("job_id", ""))
        with self.jobs_lock:
            job = self.jobs.get(target)
        if job is None:
            self._send_nack(envelope, "invalid_envelope",
                            "no such job: {0}".format(target))
            return
        job.cancel.set()
        if job.proc is not None:
            try:
                job.proc.kill()
            except Exception:
                pass
        ack = arcp.new_envelope(arcp.new_id(arcp.PREFIX_MESSAGE),
                                arcp.TYPE_ACK,
                                arcp.format_timestamp())
        ack["session_id"] = self.session_id
        ack["correlation_id"] = envelope.get("id", "")
        ack["job_id"] = target
        ack["payload"] = {}
        self.writer.send(ack)

    # ---- helpers ----------------------------------------------------------

    def _send_nack(self, in_envelope, code, message):
        nack = arcp.new_envelope(arcp.new_id(arcp.PREFIX_MESSAGE),
                                 arcp.TYPE_NACK,
                                 arcp.format_timestamp())
        nack["correlation_id"] = in_envelope.get("id", "")
        if self.session_id:
            nack["session_id"] = self.session_id
        nack["payload"] = {"code": code, "message": message}
        try:
            self.writer.send(nack)
        except Exception:
            log.exception("failed to send nack")

    def _send_tool_error(self, in_envelope, code, message):
        err = arcp.new_envelope(arcp.new_id(arcp.PREFIX_MESSAGE),
                                arcp.TYPE_TOOL_ERROR,
                                arcp.format_timestamp())
        err["session_id"] = self.session_id
        err["correlation_id"] = in_envelope.get("id", "")
        if in_envelope.get("trace_id"):
            err["trace_id"] = in_envelope["trace_id"]
        err["payload"] = {"code": code, "message": message, "retryable": False}
        try:
            self.writer.send(err)
        except Exception:
            log.exception("failed to send tool.error")


# ---- Server ----------------------------------------------------------------

SERVER_STARTED_AT = time.time()


class Server(object):
    def __init__(self, bind, port, cert, key, psk):
        self.bind = bind
        self.port = port
        self.cert = cert
        self.key = key
        self.psk = psk
        self._stop = threading.Event()

    def stop(self):
        self._stop.set()

    def run(self):
        ctx = ssl.SSLContext(ssl.PROTOCOL_TLSv1_2)
        ctx.load_cert_chain(self.cert, self.key)
        ctx.set_ciphers("ECDHE-RSA-AES256-GCM-SHA384:"
                        "ECDHE-RSA-AES128-GCM-SHA256:"
                        "ECDHE-RSA-AES128-SHA256")

        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        sock.bind((self.bind, self.port))
        sock.listen(8)
        sock.settimeout(1.0)
        log.info("xpc agent v%s listening on %s:%s (pid=%s)",
                 AGENT_VERSION, self.bind, self.port, os.getpid())

        try:
            while not self._stop.is_set():
                try:
                    client_sock, addr = sock.accept()
                except socket.timeout:
                    continue
                except OSError as exc:
                    log.warning("accept error: %s", exc)
                    continue
                try:
                    tls_sock = ctx.wrap_socket(client_sock, server_side=True)
                except (ssl.SSLError, OSError) as exc:
                    log.warning("TLS handshake failed from %s: %s", addr, exc)
                    try:
                        client_sock.close()
                    except Exception:
                        pass
                    continue
                conn = Connection(tls_sock, self.psk, self)
                t = threading.Thread(target=conn.serve)
                t.daemon = True
                t.start()
        finally:
            try:
                sock.close()
            except Exception:
                pass


# ---- CLI -------------------------------------------------------------------

def setup_logging(log_path, level):
    log.handlers = []
    log.setLevel(getattr(logging, level))
    fmt = logging.Formatter("%(asctime)s [%(levelname)s] %(name)s: %(message)s")
    try:
        fh = logging.handlers.RotatingFileHandler(
            log_path, maxBytes=LOG_MAX_BYTES, backupCount=LOG_BACKUP_COUNT)
        fh.setFormatter(fmt)
        log.addHandler(fh)
    except Exception:
        # Fall back to stderr-only if the log file is unavailable.
        pass
    sh = logging.StreamHandler()
    sh.setFormatter(fmt)
    log.addHandler(sh)


def _load_psk(path):
    with open(path, "r") as f:
        text = f.read().strip()
    psk = binascii.unhexlify(text)
    if len(psk) != 32:
        raise SystemExit("psk file must be 32 hex bytes; got {0}".format(len(psk)))
    return psk


def cmd_run(args):
    setup_logging(args.log, args.log_level)
    psk = _load_psk(args.psk_file)
    server = Server(args.bind, args.port, args.cert, args.key, psk)
    server.run()


def cmd_install_startup(args):
    import winreg
    cmdline = (
        '"{0}" "{1}" run --port {2} '
        '--cert "{3}" --key "{4}" --psk-file "{5}" --log "{6}"'
    ).format(PYTHON_EXE, os.path.abspath(__file__), args.port,
             args.cert, args.key, args.psk_file, args.log)
    key = winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE,
                         r"SOFTWARE\Microsoft\Windows\CurrentVersion\Run",
                         0, winreg.KEY_SET_VALUE)
    try:
        winreg.SetValueEx(key, "xpc_agent", 0, winreg.REG_SZ, cmdline)
    finally:
        winreg.CloseKey(key)
    print("registered HKLM\\...\\Run\\xpc_agent")
    print("  -> {0}".format(cmdline))


def cmd_remove_startup(_args):
    import winreg
    try:
        key = winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE,
                             r"SOFTWARE\Microsoft\Windows\CurrentVersion\Run",
                             0, winreg.KEY_SET_VALUE)
        try:
            winreg.DeleteValue(key, "xpc_agent")
        finally:
            winreg.CloseKey(key)
        print("removed startup entry")
    except OSError:
        print("startup entry not found")


def cmd_startup_status(_args):
    import winreg
    try:
        key = winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE,
                             r"SOFTWARE\Microsoft\Windows\CurrentVersion\Run",
                             0, winreg.KEY_READ)
        try:
            val, _ = winreg.QueryValueEx(key, "xpc_agent")
            print("installed: {0}".format(val))
        except OSError:
            print("not installed")
        finally:
            winreg.CloseKey(key)
    except OSError:
        print("not installed")


def main():
    p = argparse.ArgumentParser(prog="agent.py")
    sub = p.add_subparsers(dest="cmd")

    p_run = sub.add_parser("run", help="run the agent server")
    p_run.add_argument("--bind", default=DEFAULT_BIND)
    p_run.add_argument("--port", type=int, default=DEFAULT_PORT)
    p_run.add_argument("--cert", required=True)
    p_run.add_argument("--key", required=True)
    p_run.add_argument("--psk-file", required=True)
    p_run.add_argument("--log", default=DEFAULT_LOG_PATH)
    p_run.add_argument("--log-level", default="INFO",
                       choices=["DEBUG", "INFO", "WARNING", "ERROR"])
    p_run.set_defaults(func=cmd_run)

    p_install = sub.add_parser("install-startup",
                               help="register agent in HKLM Run key")
    p_install.add_argument("--port", type=int, default=DEFAULT_PORT)
    p_install.add_argument("--cert", default=os.path.join(DEFAULT_INSTALL_DIR, "agent.crt"))
    p_install.add_argument("--key", default=os.path.join(DEFAULT_INSTALL_DIR, "agent.key.pem"))
    p_install.add_argument("--psk-file", default=os.path.join(DEFAULT_INSTALL_DIR, "agent.key"))
    p_install.add_argument("--log", default=DEFAULT_LOG_PATH)
    p_install.set_defaults(func=cmd_install_startup)

    p_remove = sub.add_parser("remove-startup")
    p_remove.set_defaults(func=cmd_remove_startup)

    p_status = sub.add_parser("startup-status")
    p_status.set_defaults(func=cmd_startup_status)

    args = p.parse_args()
    if not args.cmd:
        p.print_help()
        sys.exit(2)
    args.func(args)


if __name__ == "__main__":
    main()
