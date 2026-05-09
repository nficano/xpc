# Phase 6c — `xpc tun` (ARCP bidirectional streams)

**Date:** 2026-05-09
**Branch:** `phase-5/host-cli` (continuing local; cumulative push pending)

---

## What landed

`xpc tun -L localPort:vmHost:vmPort` opens a TCP listener on the host. Each
accepted connection invokes the agent's `tun.connect` tool and bidirectionally
pipes bytes through ARCP `stream.chunk` envelopes (`delta_b64` binary frames).
This is the first command to exercise both directions of streaming on a
single ARCP job.

## Agent-side changes

* New tool `tun.connect`:
  * Opens a VM-side TCP socket to `arguments.host:port`.
  * Stores the socket on the running `Job` so the connection's stream.chunk
    handler can write client-sourced bytes into it.
  * Pumps VM-side recv → host via `stream.chunk(delta_b64)` envelopes on a
    fresh upstream stream id.
  * On EOF / error / cancel, closes the socket and emits `stream.close`.

* `Connection._dispatch` now also routes `stream.chunk` and `stream.close`
  envelopes from the client. They look up the matching job and:
  * `stream.chunk` -> `job.tun_socket.sendall(base64-decoded delta_b64)`
  * `stream.close` -> `job.tun_socket.shutdown(SHUT_WR)` so the upstream pump
    notices and exits.

For non-tun jobs the new dispatch branches are no-ops (a job without a
`tun_socket` attribute simply drops the chunk silently).

## Host-side `internal/cli/tun.go`

* Listener on `127.0.0.1:<localPort>`.
* For each accepted local conn, opens an ARCP session, sends
  `tool.invoke{tool: "tun.connect", arguments: {host, port}}`, then runs:
  * **reader** goroutine: decodes envelopes, routes `stream.chunk` (decode
    `delta_b64`, write to local conn), `stream.close` (half-close local),
    terminal types (close everything).
  * **forwarder** goroutine: waits for `job_id` via a `jobReady` channel,
    then loops `local.Read(buf)` and emits `stream.chunk` envelopes on a
    fresh downstream stream id. On EOF, emits `stream.close`.
* A single `writeMu` mutex serialises envelope writes so concurrent
  goroutines (forwarder + cancel sender) don't interleave bytes on the TLS
  conn.

A subtle race surfaced during real-VM testing: the forwarder must wait for
`job.accepted` to populate `job_id` before it sends the first `stream.chunk`.
Without that gate the agent silently drops the chunk because the lookup by
empty `job_id` finds no job. Fixed with a one-shot `jobReady` channel.

## Real-VM verification

```text
$ ./bin/xpc bootstrap          # redeploy agent so the VM gets tun.connect
... bootstrap complete ...

$ ./bin/xpc tun -L 19578:127.0.0.1:9578 &
xpc tun: 127.0.0.1:19578 -> 127.0.0.1:9578 (Ctrl-C to stop)

$ python3 - <<'PY'
import json, socket, struct
s = socket.create_connection(("127.0.0.1", 19578), timeout=10)
msg = {"id":"tun-test","type":"request","action":"ping","params":{}}
p = json.dumps(msg).encode()
s.sendall(struct.pack("!I", len(p)) + p)
hdr = s.recv(4)
n = struct.unpack("!I", hdr)[0]
buf = b""
while len(buf) < n:
    buf += s.recv(n - len(buf))
print("xpctl response:", json.loads(buf))
PY

xpctl response: {'status': 'ok', 'type': 'response', 'data':
                 {'uptime': 72791.625, 'pong': True},
                 'error': None, 'id': 'tun-test'}
```

The probe sent xpctl's length-prefixed-JSON ping through the tunnel and
received the canonical xpctl pong (uptime 72791 s = ~20 hours). Bytes round-
tripped both ways through the agent's ARCP streams.

Agent log shows the dispatch:
```
2026-05-09 12:40:07,484 [INFO] xpc.agent: tun.connect [job=job_ryp...] -> 127.0.0.1:9578
2026-05-09 12:40:12,468 [INFO] xpc.agent: connection end: ('172.16.20.125', 50441)
```

## Phase 6c exit gate: PASSED

- [x] Agent-side `tun.connect` tool with bidirectional stream routing.
- [x] Host-side `xpc tun -L` cobra command with reader/forwarder/cancel pumps.
- [x] Existing test suite (42 Python + Go suites) still green after agent dispatch additions.
- [x] Real-VM verification: xpctl ping over the tunnel.
- [x] Session log captured (this file).

## Still deferred

* `xpc tun -R` (remote-to-local reverse forwarding).
* `xpc dbg attach|run|server` (long-running debugger sessions).
* `xpc trace start|stop|pull` (procmon wrapper).
* `xpc ghidra start/stop`, `xpc ida start/stop` (server lifecycle + tunnel,
  now buildable on top of `xpc tun`).
* `xpc snap` (Proxmox API; user input still pending).
* `xpc daemon` (host-side multiplex; latency optimization).
