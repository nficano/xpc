"""In-process tests for the xpc agent's connection-and-dispatch layer.

These tests use a `socketpair` so we can drive the agent's `Connection.serve()`
loop without standing up a TLS listener. TLS handshake is exercised in the
real-VM Phase 4 verification (see docs/sessions/phase-4-agent.md).
"""

import contextlib
import os
import socket
import sys
import threading
import time
from typing import Any

import pytest

HERE = os.path.dirname(os.path.abspath(__file__))
AGENT_DIR = os.path.dirname(HERE)
sys.path.insert(0, AGENT_DIR)

import arcp  # noqa: E402

import agent  # noqa: E402

PSK = b"\x00" * 32


class Pipe:
    """Bundle a socket with persistent read/write file objects.

    ``socket.makefile()`` returns buffered file objects; each call returns
    a distinct buffer. Calling ``makefile()`` per send/recv leaks pre-fetched
    bytes when the buffered reader is GC'd. We keep ONE ``rfile``/``wfile``
    per socket for the duration of the test.

    :param sock: a connected socket (e.g. one half of ``socket.socketpair``).
    """

    def __init__(self, sock: socket.socket) -> None:
        self.sock = sock
        self.rfile = sock.makefile("rb")
        self.wfile = sock.makefile("wb")

    def close(self) -> None:
        """Close the read/write file objects and the socket, ignoring errors."""
        for closer in (self.rfile, self.wfile, self.sock):
            with contextlib.suppress(Exception):
                closer.close()


@pytest.fixture
def session():
    """Yield (Pipe, server_thread). Tears down on exit."""
    a, b = socket.socketpair()
    pipe = Pipe(a)
    conn = agent.Connection(b, PSK, server=None)
    t = threading.Thread(target=conn.serve)
    t.daemon = True
    t.start()
    try:
        yield pipe, t
    finally:
        pipe.close()
        t.join(timeout=2)


def _send(pipe: Pipe, envelope: dict[str, Any], psk: bytes = PSK) -> None:
    """Sign *envelope* with *psk* and write it as a framed message to *pipe*."""
    arcp.sign(envelope, psk)
    arcp.write_frame(pipe.wfile, envelope)
    pipe.wfile.flush()


def _recv(pipe: Pipe, psk: bytes = PSK, timeout: float = 3.0) -> dict[str, Any] | None:
    """Read one framed envelope from *pipe* and verify its signature.

    :returns: the parsed envelope, or ``None`` on clean EOF.
    """
    pipe.sock.settimeout(timeout)
    env = arcp.read_frame(pipe.rfile)
    if env is not None:
        arcp.verify_sig(env, psk)
    return env


def _new(msg_type: str, payload: dict[str, Any] | None = None, **fields: str) -> dict[str, Any]:
    """Build a fresh envelope of *msg_type* with optional *payload* and fields.

    Empty/false-y *fields* values are omitted, matching the agent's own
    envelope-building idiom.
    """
    e = arcp.new_envelope(arcp.new_id(arcp.PREFIX_MESSAGE), msg_type, arcp.format_timestamp())
    if payload is not None:
        e["payload"] = payload
    for k, v in fields.items():
        if v:
            e[k] = v
    return e


def _drain_until(
    pipe: Pipe, terminal_types: tuple[str, ...], timeout: float = 3.0
) -> list[dict[str, Any]]:
    """Read envelopes from *pipe* until one of *terminal_types* arrives.

    :returns: the list of envelopes received, including the terminal one if
        seen. Returns whatever was received so far on timeout or EOF.
    """
    seen: list[dict[str, Any]] = []
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            env = _recv(pipe, timeout=max(0.1, deadline - time.time()))
        except (socket.timeout, OSError):
            break
        if env is None:
            break
        seen.append(env)
        if env["type"] in terminal_types:
            return seen
    return seen


def test_session_open_returns_session_accepted(session):
    pipe, _ = session
    open_env = _new(
        arcp.TYPE_SESSION_OPEN,
        {
            "client": {"name": "test", "version": "0.0"},
            "capabilities": {
                "streaming": True,
                "binary_streams": True,
                "durable_jobs": True,
                "checkpoints": True,
            },
        },
    )
    _send(pipe, open_env)
    resp = _recv(pipe)
    assert resp["type"] == arcp.TYPE_SESSION_ACCEPTED
    assert resp["correlation_id"] == open_env["id"]
    assert resp["payload"]["session_id"].startswith("sess_")
    caps = resp["payload"]["capabilities"]
    assert caps["streaming"] is True
    assert caps["binary_streams"] is True
    assert caps["durable_jobs"] is False
    assert caps["checkpoints"] is False


def test_ping_returns_pong(session):
    pipe, _ = session
    _send(
        pipe,
        _new(
            arcp.TYPE_SESSION_OPEN,
            {"client": {"name": "t", "version": "0"}, "capabilities": {"streaming": True}},
        ),
    )
    accepted = _recv(pipe)
    sid = accepted["session_id"]

    ping = _new(arcp.TYPE_PING, session_id=sid)
    _send(pipe, ping)
    pong = _recv(pipe)
    assert pong["type"] == arcp.TYPE_PONG
    assert pong["correlation_id"] == ping["id"]
    assert pong["session_id"] == sid


def test_auth_failure_closes_connection(session):
    pipe, t = session
    bad = _new(arcp.TYPE_PING)
    arcp.sign(bad, b"\x01" * 32)
    arcp.write_frame(pipe.wfile, bad)
    pipe.wfile.flush()

    nack = _recv(pipe)
    assert nack["type"] == arcp.TYPE_NACK
    assert nack["payload"]["code"] == "auth_failed"
    # Connection should close after auth failure.
    assert _recv(pipe) is None
    t.join(timeout=2)


def test_unsupported_type_returns_nack(session):
    pipe, _ = session
    _send(pipe, _new(arcp.TYPE_SESSION_OPEN, {"capabilities": {}}))
    accepted = _recv(pipe)
    sid = accepted["session_id"]

    bogus = _new("does.not.exist", session_id=sid)
    _send(pipe, bogus)
    nack = _recv(pipe)
    assert nack["type"] == arcp.TYPE_NACK
    assert nack["payload"]["code"] == "unsupported_type"


def test_tool_invoke_unknown_tool_returns_tool_error(session):
    pipe, _ = session
    _send(pipe, _new(arcp.TYPE_SESSION_OPEN, {"capabilities": {}}))
    accepted = _recv(pipe)
    sid = accepted["session_id"]

    invoke = _new(
        arcp.TYPE_TOOL_INVOKE, {"tool": "does.not.exist", "arguments": {}}, session_id=sid
    )
    _send(pipe, invoke)

    # Expect a tool.error followed by no job lifecycle (handler is missing
    # before the job runs). At minimum there's the tool.error.
    err = _recv(pipe)
    assert err["type"] == arcp.TYPE_TOOL_ERROR
    assert err["payload"]["code"] == "TOOL_NOT_FOUND"


def test_tool_invoke_before_session_open_is_rejected(session):
    pipe, _ = session
    invoke = _new(arcp.TYPE_TOOL_INVOKE, {"tool": "exec", "arguments": {"cmd": "echo hi"}})
    _send(pipe, invoke)
    nack = _recv(pipe)
    assert nack["type"] == arcp.TYPE_NACK
    assert nack["payload"]["code"] == "invalid_envelope"


def test_tools_list_returns_descriptors(session):
    pipe, _ = session
    _send(pipe, _new(arcp.TYPE_SESSION_OPEN, {"capabilities": {}}))
    accepted = _recv(pipe)
    sid = accepted["session_id"]

    req = _new(arcp.TYPE_TOOLS_LIST, session_id=sid)
    _send(pipe, req)
    resp = _recv(pipe)

    assert resp["type"] == arcp.TYPE_TOOLS_RESULT
    assert resp["correlation_id"] == req["id"]
    assert resp["session_id"] == sid

    tools = resp["payload"]["tools"]
    names = [t["name"] for t in tools]
    # Every dispatchable tool must have a descriptor.
    for name in agent.TOOLS:
        assert name in names, f"missing descriptor for {name}"

    for descriptor in tools:
        assert descriptor["name"] in agent.TOOLS, (
            "descriptor names a tool that isn't registered: {}".format(descriptor["name"])
        )
        assert descriptor.get("description")
        assert descriptor["input_schema"]["type"] == "object"


def test_tools_list_before_session_open_is_rejected(session):
    pipe, _ = session
    _send(pipe, _new(arcp.TYPE_TOOLS_LIST))
    nack = _recv(pipe)
    assert nack["type"] == arcp.TYPE_NACK
    assert nack["payload"]["code"] == "invalid_envelope"


def test_agent_info_tool_returns_metadata(session):
    pipe, _ = session
    _send(pipe, _new(arcp.TYPE_SESSION_OPEN, {"capabilities": {}}))
    accepted = _recv(pipe)
    sid = accepted["session_id"]

    invoke = _new(arcp.TYPE_TOOL_INVOKE, {"tool": "agent.info", "arguments": {}}, session_id=sid)
    _send(pipe, invoke)
    seen = _drain_until(pipe, (arcp.TYPE_JOB_COMPLETED, arcp.TYPE_JOB_FAILED))

    types = [env["type"] for env in seen]
    assert arcp.TYPE_JOB_ACCEPTED in types
    assert arcp.TYPE_JOB_STARTED in types
    assert arcp.TYPE_TOOL_RESULT in types
    assert arcp.TYPE_JOB_COMPLETED in types

    result = next(e for e in seen if e["type"] == arcp.TYPE_TOOL_RESULT)
    assert result["payload"]["agent"]["name"] == "xpc"
    assert result["payload"]["agent"]["version"] == agent.AGENT_VERSION


def test_tool_error_is_returned_as_tool_error_envelope():
    """ToolError raised by a handler becomes a tool.error envelope."""
    a, b = socket.socketpair()
    pipe = Pipe(a)
    conn = agent.Connection(b, PSK, server=None)
    t = threading.Thread(target=conn.serve)
    t.daemon = True
    t.start()

    original_tools = dict(agent.TOOLS)

    def _always_fails(arguments, ctx, job):
        raise agent.ToolError("BAD_THING", "oops", retryable=True)

    agent.TOOLS["always_fails"] = _always_fails

    try:
        _send(pipe, _new(arcp.TYPE_SESSION_OPEN, {"capabilities": {}}))
        accepted = _recv(pipe)
        sid = accepted["session_id"]

        _send(
            pipe,
            _new(arcp.TYPE_TOOL_INVOKE, {"tool": "always_fails", "arguments": {}}, session_id=sid),
        )
        seen = _drain_until(pipe, (arcp.TYPE_JOB_FAILED, arcp.TYPE_JOB_COMPLETED))
        types = [env["type"] for env in seen]
        assert arcp.TYPE_TOOL_ERROR in types
        assert arcp.TYPE_JOB_FAILED in types

        err = next(e for e in seen if e["type"] == arcp.TYPE_TOOL_ERROR)
        assert err["payload"]["code"] == "BAD_THING"
        assert err["payload"]["message"] == "oops"
        assert err["payload"]["retryable"] is True
    finally:
        agent.TOOLS.clear()
        agent.TOOLS.update(original_tools)
        pipe.close()
        t.join(timeout=2)
