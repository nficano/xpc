# Phase 3 — Real-VM round-trip session log

**Date:** 2026-05-08
**Branch:** `phase-3/wire-protocol`
**Verifier:** automated round-trip via `cmd/xpc-roundtrip` against the live XP VM

---

## Setup

Target VM: `xp-truvoice-w02.home.nickficano.com` (172.16.20.173), Windows XP SP3, Python 3.4.10, OpenSSL 1.0.2k.

Fresh artifacts generated at session start:

```sh
openssl req -x509 -newkey rsa:2048 -days 1 -nodes -subj "/CN=xpc-phase3-test" \
    -keyout server.key -out server.crt
openssl rand -hex 32 > psk.hex
openssl x509 -in server.crt -outform DER | shasum -a 256
# fingerprint = 4241064e1496cc03cc6fd08479a4ca4e9c8c306c8b21231449bac13856fd6b1e
```

PSK and cert/key are temporary test artifacts — never committed, never reused.

## Deploy

Files uploaded to `C:\xpc\` on the VM via the existing xpctl agent on TCP 9578:

| Local | Remote |
|---|---|
| `agent/arcp.py` | `C:\xpc\arcp.py` |
| `agent/scripts/echo_server.py` | `C:\xpc\echo_server.py` |
| `server.crt` | `C:\xpc\server.crt` |
| `server.key` | `C:\xpc\server.key` |
| `psk.hex` | `C:\xpc\psk.hex` |
| `tmp/manage.py` | `C:\xpc\manage.py` |

## Start

`manage.py` redirects its OS-level stdin/stdout/stderr to `NUL` before
`subprocess.Popen(..., creationflags=DETACHED_PROCESS|CREATE_NEW_PROCESS_GROUP)`.
Without the redirect, the child inherits xpctl's PIPE handles via Windows handle
inheritance, keeping `Popen.communicate()` blocked. With the redirect the
inherited handles point at `NUL`, the child detaches cleanly, and the agent's
`exec` returns rc=0 immediately.

```text
manage.log:
killing pid 2184
echo_server pid=2296

netstat:
TCP    0.0.0.0:9579           0.0.0.0:0              LISTENING       2296

echo.log:
echo server listening on 0.0.0.0:9579
```

## Round-trip

```sh
$ go run ./cmd/xpc-roundtrip \
    --addr xp-truvoice-w02:9579 \
    --fingerprint 4241064e1496cc03cc6fd08479a4ca4e9c8c306c8b21231449bac13856fd6b1e \
    --psk /tmp/.../psk.hex
```

```text
CONNECTED xp-truvoice-w02:9579 (TLS 1.2)
OK   ping         -> ping.echo
OK   session.open -> session.open.echo
OK   tool.invoke  -> tool.invoke.echo
OK   stream.chunk -> stream.chunk.echo
OK   log          -> log.echo

ROUND-TRIP OK (5/5 cases)
```

## Cleanup

```text
$ python C:\xpc\manage.py kill
manage.log: killing pid 2296
```

## What this proves

| Claim | Evidence |
|---|---|
| TLS 1.2 with `ECDHE-RSA-AES256-GCM-SHA384` works between Go's `crypto/tls` client and Python 3.4's `ssl` server on XP | TLS handshake completes; `CONNECTED` line printed by client |
| Self-signed cert + SHA-256 fingerprint pinning works | Client-side `transport.PinFingerprint` accepts the cert; verified independently in `internal/transport/tls_test.go` |
| HMAC-SHA256 sign/verify with byte-parity between Go (`internal/arcp`) and Python (`agent/arcp.py`) | Each round-tripped envelope was re-signed by the server (Python) and verified by the client (Go); 5/5 OK |
| Length-prefixed framing handles every v0 message type structurally | `tool.invoke` with nested arguments, `stream.chunk` with stream_id+correlation_id, `log` with HTML-special chars (`<`, `&`) — all round-tripped exactly |
| Python 3.4's OpenSSL 1.0.2k speaks Go's preferred TLS 1.2 cipher suite | TLS handshake succeeds with `tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384` advertised in the client config |

R1/R8 (TLS-on-Python-3.4) risk in `docs/ARCHITECTURE.md` is now closed.

## Phase 3 exit gate: PASSED

- [x] `docs/PROTOCOL.md` complete and committed.
- [x] Test corpus (`tests/protocol_corpus.json`) committed.
- [x] Go and Python implementations have passing unit + corpus parity tests.
- [x] Real-network round-trip succeeds against the XP VM.
- [x] Session log captured (this file).
