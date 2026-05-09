# xpc Wire Protocol

> Status: **v0** — frozen for the duration of the v0 release cycle. Subset of [ARCP RFC 0001](protocol/RFC-0001.md).
>
> This document defines exactly what bytes go on the wire between an `xpc` host CLI and an `xpc serve` agent on a Windows XP VM. It is the canonical reference for `internal/arcp` (Go) and `agent/arcp.py` (Python 3.4). When the doc and the code disagree, the doc wins and the code is bugged.

---

## 1. Scope

The protocol layers, top down:

```
ARCP envelope    (JSON, this doc)
HMAC-SHA256      (auth.sig over canonical envelope minus auth.sig)
length-prefixed  (4-byte big-endian uint32 length, then envelope bytes)
TLS 1.2          (cert pinned by SHA-256 fingerprint)
TCP              (default port 9578)
```

A connection always carries exactly one xpc session. There is no multiplexing of sessions over a single TCP connection in v0.

Live VM probe (2026-05-08): Python 3.4.10 on the target ships OpenSSL 1.0.2k, supports TLS 1.2 with `ECDHE-RSA-AES{128,256}-GCM-SHA{256,384}` cipher suites — i.e., the same set Go's `crypto/tls` accepts. Risk R1/R8 in `docs/ARCHITECTURE.md` is closed.

---

## 2. Framing

```
+-------------+-------------+   +-------------+
| length (4B) |  envelope   |   | length (4B) |  envelope ...
| big-endian  |  (UTF-8     |   |             |
| uint32      |   JSON)     |   |             |
+-------------+-------------+   +-------------+
```

- `length` is the byte count of the JSON envelope only (does not include the 4 length bytes themselves).
- Maximum envelope size: **50 MiB** (52,428,800 bytes). Senders MUST NOT exceed this; receivers MUST close the connection on overlength.
- The envelope is UTF-8 encoded. JSON values inside it follow [RFC 8259](https://www.rfc-editor.org/rfc/rfc8259).
- Length is encoded `binary.BigEndian.PutUint32(...)` in Go and `struct.pack("!I", n)` in Python.

Receivers MUST read exactly `length` bytes into the envelope buffer; partial reads are expected on TCP and the codec MUST loop until the buffer is full or EOF is reached.

---

## 3. Envelope

```jsonc
{
  "arcp":           "1.0",
  "id":             "msg_01HABCDEF...",   // 26-char base32, prefix encodes message family
  "type":           "tool.invoke",         // see §6
  "session_id":     "sess_01H...",         // present after session.open accepted
  "job_id":         "job_01H...",          // present after job.accepted
  "stream_id":      "str_01H...",          // present for stream.* messages
  "trace_id":       "tr_01H...",           // recommended; stable across one xpc <cmd>
  "span_id":        "sp_01H...",           // recommended
  "correlation_id": "msg_01H...",          // id of the message this responds to
  "causation_id":   "msg_01H...",          // id of the message that caused this one
  "timestamp":      "2026-05-08T18:21:00.000000Z",  // RFC 3339 with µs precision
  "auth": {
    "alg": "HMAC-SHA256",
    "kid": "v0",
    "sig": "deadbeef..."                   // hex(hmac(psk, canonical_minus_sig))
  },
  "payload": { /* type-specific, see §6 */ }
}
```

### 3.1 Field rules

| Field | Required | Notes |
|---|---|---|
| `arcp` | always | Must be exactly `"1.0"` in v0 |
| `id` | always | Globally unique; idempotency key. Receivers SHOULD dedupe by `id` |
| `type` | always | Lowercase, dot-separated namespace. Unknown types → `nack` |
| `session_id` | conditional | Present once a session is established |
| `job_id` | conditional | Present after `job.accepted` |
| `stream_id` | conditional | Required for `stream.*` messages |
| `trace_id` | recommended | Stable for one user-visible request; if absent, receiver assigns one |
| `span_id` | recommended | Span within the trace |
| `parent_span_id` | optional | Parent span for trace tree assembly |
| `correlation_id` | optional | Set on responses to point at the request id |
| `causation_id` | optional | Set on emitted events to point at the message that caused them |
| `timestamp` | always | RFC 3339 / ISO 8601, UTC, with microsecond precision and trailing `Z` |
| `source` | optional | Logical sender id (`"client"`, `"runtime"`, agent name) |
| `target` | optional | Logical recipient id |
| `auth` | always | See §4 |
| `payload` | always | Object; may be `{}` for messages without parameters |

### 3.2 ID format

`<prefix>_<26-base32>` where `<prefix>` ∈ `{msg, sess, job, str, tr, sp}`. The base32 part is generated from 16 random bytes encoded via Crockford base32 (or a simpler stdlib base32 in lowercase). For v0 the only requirement is uniqueness within a deployment lifetime; we do not require strict ULID monotonicity.

Implementations MUST NOT parse the base32 portion for ordering or routing; treat it as opaque.

### 3.3 Timestamp format

```
2026-05-08T18:21:00.000000Z
```

UTC, microsecond precision, trailing `Z`. Receivers SHOULD reject timestamps more than 5 minutes in the future or more than 24 hours in the past as an anti-replay measure (Phase 3 enforcement is **strict-window: ±5 min**).

---

## 4. Authentication

Every envelope (with the exception of `session.open` from a brand-new client that has not yet learned the agent's `kid` — see §4.4) carries an HMAC-SHA256 signature in `auth.sig`.

### 4.1 Pre-shared key (PSK)

- 32 random bytes (256 bits), base64-encoded for storage.
- Generated at `xpc bootstrap` time on the host.
- Provisioned to the VM by SCP'ing it to `C:\xpc\agent.key` (file ACL: only the agent's user can read).
- Stored on the host in `~/.xpc/credentials` under the relevant profile.

### 4.2 Canonicalization

To compute the signature, both ends construct the **canonical envelope bytes**:

1. Make a deep copy of the envelope.
2. Replace `auth.sig` with the empty string. Keep `auth.alg` and `auth.kid` as-is.
3. Encode the result as UTF-8 JSON with **sorted keys at every level** and **no whitespace** (`json.dumps(obj, sort_keys=True, separators=(',', ':'))` in Python; equivalent JCS-shaped encoding in Go).
4. The resulting bytes are the canonical input to HMAC.

Both Go and Python implementations MUST produce byte-identical canonical bytes for the same envelope. This is enforced by `tests/protocol_corpus.json` (see §10).

### 4.3 Sign / verify

```
sig = lower_hex(HMAC_SHA256(psk_bytes, canonical_envelope_bytes))
```

- Senders compute `sig` as above and place it in `auth.sig` before sending.
- Receivers extract `auth.sig`, recompute, compare with `hmac.compare_digest` (Python) / `subtle.ConstantTimeCompare` (Go). Mismatch → close connection with no response.

### 4.4 Key rotation

- `auth.kid` identifies which PSK was used. v0 always uses `"v0"`.
- Future rotation: `xpc rotate-key` provisions a new key under `kid = "v1"`, the agent accepts both during an overlap window, then `v0` retires.
- v0 implementations MUST reject envelopes with `auth.kid != "v0"`.

### 4.5 Anti-replay

- `id` is the idempotency key. Receivers SHOULD remember seen IDs for the lifetime of a session and reject duplicates with `nack` carrying `code: "duplicate_id"`.
- `timestamp` is bounded to ±5 minutes from receiver's clock; outside the window → `nack` with `code: "timestamp_out_of_window"`.

---

## 5. TLS

### 5.1 Version and cipher suites

- TLS 1.2 only.
- Cipher suite preference (server-side):
  1. `TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384`
  2. `TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256`
  3. `TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA384` (fallback)
  4. `TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256` (fallback)

The first two are confirmed working on the live VM's Python 3.4.10 OpenSSL 1.0.2k.

### 5.2 Cert and trust

- The agent generates a self-signed RSA 2048 cert at `xpc bootstrap` time and stores it as `C:\xpc\agent.pem` (cert) and `C:\xpc\agent.key.pem` (private key, ACL'd).
- The host pins the cert by SHA-256 fingerprint (`fingerprint = sha256:AB:CD:...`) in `~/.xpc/config` per profile.
- Hostname verification is **disabled**; trust is anchored entirely on the fingerprint.
- First connect to a profile with no pinned fingerprint → TOFU prompt at the CLI.
- Cert regeneration: `xpc bootstrap --regenerate-cert` re-pins.

### 5.3 Client and server roles

- Server (agent): `ssl.SSLContext(ssl.PROTOCOL_TLSv1_2)`, `ctx.load_cert_chain(certfile, keyfile)`, `ctx.set_ciphers(...)`, wraps the accepted socket with `ctx.wrap_socket(sock, server_side=True)`.
- Client (host): Go `tls.Config{MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12, InsecureSkipVerify: true, VerifyConnection: pinFingerprint}`.

The client's `InsecureSkipVerify: true` is intentional; the verification job is done by `VerifyConnection` checking the leaf cert's SHA-256 fingerprint against the pinned value.

---

## 6. Message types (v0)

Implementations MUST recognize all of the following types. Behavior beyond encode/decode varies by phase; Phase 3 only requires structural support.

### 6.1 Control

#### `session.open`
Payload:
```json
{
  "client": {"name": "xpc", "version": "0.0.0-dev"},
  "capabilities": {
    "streaming": true,
    "binary_streams": true,
    "durable_jobs": false,
    "checkpoints": false,
    "agent_handoff": false
  }
}
```

#### `session.accepted`
Payload:
```json
{
  "session_id": "sess_01H...",
  "agent": {"name": "xpc", "version": "0.0.0-dev", "build": "..."},
  "capabilities": { /* server-side intersection */ }
}
```

#### `session.close`
Payload: `{ "reason": "client_done" }` (free-form string, optional).

#### `ping`
Payload: `{}`.

#### `pong`
Payload: `{ "correlation_id": "msg_..." }` (in addition to the envelope-level field).

#### `ack`
Payload: `{}`. Optional positive ack of a command.

#### `nack`
Payload:
```json
{
  "code": "auth_failed" | "duplicate_id" | "timestamp_out_of_window" |
          "invalid_envelope" | "unsupported_type" | "rate_limited",
  "message": "..."
}
```

#### `cancel`
Payload: `{ "job_id": "job_..." }` (also in envelope).

#### `permission.request` / `permission.grant` / `permission.deny`
Payload (request):
```json
{
  "permission": "shell.execute",
  "resource":   "process:cmd.exe",
  "operation":  "spawn",
  "reason":     "execute user-supplied command",
  "requested_lease_seconds": 300
}
```

Grant adds `lease_id` and `expires_at`. Deny adds `reason`.

### 6.2 Execution

#### `tool.invoke`
Payload:
```json
{
  "tool": "exec",
  "arguments": { /* tool-specific */ }
}
```

#### `tool.result`
Payload: `{ /* tool-specific */ }`.

#### `tool.error`
Payload:
```json
{
  "code": "EXEC_FAILED" | "...",
  "message": "...",
  "retryable": false
}
```

#### `tools.list`
Payload: `{}`. Sent inside an established session to ask the agent to enumerate every tool registered with its dispatcher. The response is a single `tools.list.result` whose `correlation_id` points at this message's `id`.

#### `tools.list.result`
Payload:
```json
{
  "tools": [
    {
      "name":         "exec",
      "description":  "Run a command on the VM and stream stdout/stderr.",
      "input_schema": { /* JSON Schema describing tool.invoke arguments */ }
    }
  ]
}
```

Each entry in `tools` is an ARCP **capability descriptor**. `name` matches what `tool.invoke` accepts in `payload.tool`. `input_schema` is JSON Schema (Draft 2020-12 subset) describing the structure of the matching `payload.arguments`. Bridges that adapt ARCP to MCP MAY pass `input_schema` through unchanged as MCP's `inputSchema`. Senders that don't recognize a descriptor MUST skip it without erroring.

#### `job.accepted` / `job.started` / `job.progress` / `job.completed` / `job.failed` / `job.cancelled`
Standard job lifecycle. Payloads are job-state-specific; see ARCP RFC §9.

### 6.3 Streaming

#### `stream.open`
Payload:
```json
{
  "stream_id":    "str_01H...",
  "content_type": "text/plain" | "application/octet-stream" | "...",
  "channel":      "stdout" | "stderr" | "..."
}
```

#### `stream.chunk`
Payload (text):
```json
{ "delta": "Volume in drive C is..." }
```

Payload (binary):
```json
{ "delta_b64": "base64-bytes" }
```

Both forms are valid; senders pick based on `content_type` declared in `stream.open`. Receivers MUST handle both.

#### `stream.close`
Payload: `{ "reason": "complete" }`.

#### `stream.error`
Payload: `{ "code": "...", "message": "..." }`.

### 6.4 Event

#### `log`
Payload:
```json
{
  "level":   "debug" | "info" | "warn" | "error",
  "message": "...",
  "fields":  { /* arbitrary JSON object */ }
}
```

---

## 7. Capability negotiation

Sent in `session.open` payload; agent responds in `session.accepted` with the *intersection* of what the client requested and what the server supports.

```json
{
  "streaming":      true,
  "binary_streams": true,
  "durable_jobs":   false,
  "checkpoints":    false,
  "agent_handoff":  false
}
```

v0 server-side support: `streaming` and `binary_streams` are `true`; the rest are `false`. Senders MUST gate use of optional features behind capability checks.

---

## 8. Error semantics

- Protocol-level errors (auth, framing, malformed JSON, unsupported type) → close connection or return `nack`.
- Tool-level errors → `tool.error` with `code` (machine-readable) and `message` (human-readable). `retryable: bool` advises clients whether to back off and retry.
- Job-level failures → `job.failed` with structured error in payload.
- Streams that go wrong mid-flight → `stream.error` then `stream.close`.

---

## 9. Idempotency and dedup

- Senders MUST give each envelope a unique `id`.
- Receivers SHOULD remember seen `id` values for the session and reject duplicates with `nack code: "duplicate_id"`.
- Tool implementations SHOULD be idempotent where possible (e.g. `reg set` setting a value to its current value is a no-op; `svc start` for an already-running service returns success).

---

## 10. Test corpus

`tests/protocol_corpus.json` contains golden envelopes for every v0 message type, with their expected canonical bytes (UTF-8 hex), expected length-prefixed framing bytes (UTF-8 hex), and expected HMAC over a fixed PSK (32 zero bytes for the corpus only — never a real key).

Both Go and Python test suites load this corpus and assert byte-for-byte parity. Adding a new message type requires updating the corpus.

---

## 11. Reserved / deferred (post-v0)

The following are defined in ARCP RFC 0001 and explicitly out of scope for v0. Adding them later is non-breaking:

- `resume` and `job.checkpoint` / `checkpoint.restore` — durable jobs across reconnects.
- `backpressure` envelope — flow control on streams.
- `agent.delegate` / `agent.handoff` — multi-agent coordination.
- `metric` / `trace.span` events — OpenTelemetry export.
- `workflow.start` / `workflow.complete` — multi-step workflows.

The envelope shape already accommodates these; only the dispatch handlers are missing.
