# RFC 0001 — Agent Runtime Control Protocol (ARCP)

**Status:** Draft

**Authors:** Nick Ficano et al.

## Abstract

ARCP (Agent Runtime Control Protocol) is a transport-agnostic, schema-first protocol for secure, observable, streaming-native execution of tools, resources, workflows, and agent-to-agent interactions.

ARCP is designed to complement existing capability-discovery protocols such as Model Context Protocol (MCP), while addressing gaps in:

- runtime execution
- streaming
- cancellation
- resumability
- durable jobs
- multi-agent orchestration
- state synchronization
- permissions
- tracing
- event delivery
- sandbox enforcement
- capability negotiation

ARCP is not intended to replace MCP. Instead:

- **MCP** defines *what* exists.
- **ARCP** defines *how* execution occurs.

---

## 1. Goals

### 1.1 Primary Goals

ARCP aims to provide:

- Transport-independent execution semantics
- Durable asynchronous job execution
- Streaming-first interactions
- Typed capability negotiation
- Structured observability and tracing
- Secure sandboxed execution
- Agent-to-agent interoperability
- Backpressure-aware streaming
- Resumable workflows
- Unified event propagation
- Stateful and stateless execution modes
- Incremental partial responses

---

## 2. Non-Goals

ARCP intentionally does **not** define:

- LLM prompt formats
- Vector database standards
- Model architectures
- Tool schema formats
- UI rendering systems
- Authentication provider implementations
- Persistence engine requirements

ARCP **MAY** integrate with these systems.

---

## 3. Terminology

| Term | Definition |
|------|------------|
| Agent | Autonomous system capable of executing work |
| Runtime | Execution environment implementing ARCP |
| Tool | Executable function/resource |
| Session | Stateful interaction scope |
| Stream | Incremental event/data channel |
| Job | Durable asynchronous execution |
| Capability | Declared runtime feature |
| Envelope | Canonical ARCP message container |
| Transport | Underlying communication layer |
| Lease | Temporary execution ownership |

---

## 4. Design Principles

### 4.1 Transport Agnostic

ARCP **MUST** support:

- stdio
- WebSocket
- HTTP/2
- QUIC
- Unix sockets
- named pipes
- message queues

without changing protocol semantics.

### 4.2 Streaming Native

Streaming is a first-class primitive.

All invocations **MAY**:

- stream partial results
- emit events
- emit logs
- emit progress
- emit checkpoints

### 4.3 Durable Execution

Long-running jobs **MUST** support:

- persistence
- recovery
- resumability
- cancellation
- heartbeats

### 4.4 Typed Contracts

All protocol messages **MUST**:

- validate against schemas
- include explicit versions
- support negotiation

### 4.5 Event Driven

Everything is modeled as events.

Examples:

- invocation started
- progress updated
- partial response
- checkpoint saved
- cancellation requested
- tool completed
- agent transferred
- permission denied

---

## 5. Architecture

```text
+-----------------------+
| Capability Layer      |
| (MCP Compatible)      |
+-----------------------+
+-----------------------+
| ARCP Runtime Layer    |
| - Sessions            |
| - Streams             |
| - Jobs                |
| - Events              |
| - Permissions         |
| - Tracing             |
+-----------------------+
+-----------------------+
| Transport Layer       |
| HTTP/WebSocket/etc    |
+-----------------------+
```

---

## 6. Core Protocol Concepts

### 6.1 Envelope

All ARCP messages **MUST** use a canonical envelope.

Example:

```json
{
  "arcp": "1.0",
  "id": "msg_01JABC",
  "type": "job.progress",
  "session_id": "sess_123",
  "job_id": "job_456",
  "trace_id": "trace_789",
  "timestamp": "2026-05-07T21:30:00Z",
  "payload": {}
}
```

#### 6.1.1 Envelope Fields

| Field | Required | Description |
|-------|----------|-------------|
| `arcp` | yes | Protocol version understood by the sender |
| `id` | yes | Globally unique message id; also used as the retry idempotency key |
| `type` | yes | Message type, such as `tool.invoke`, `job.progress`, or `stream.chunk` |
| `timestamp` | yes | Sender timestamp in RFC 3339 format |
| `source` | no | Logical sender id, such as client, runtime, or agent name |
| `target` | no | Logical recipient id, such as runtime, tool host, or agent name |
| `session_id` | conditional | Required once a session exists |
| `job_id` | conditional | Required for durable job events |
| `stream_id` | conditional | Required for stream events |
| `trace_id` | recommended | Stable id for one user-visible request or workflow |
| `span_id` | recommended | Span id for the current operation |
| `parent_span_id` | no | Parent span id when the message is part of a trace tree |
| `correlation_id` | no | Id of the command or request this message answers |
| `causation_id` | no | Id of the message that directly caused this message |
| `payload` | yes | Type-specific body validated by the message schema |

Receivers **SHOULD** treat message ids as idempotency keys. Retried messages with the same id **MUST NOT** execute twice. Runtimes **SHOULD** preserve `correlation_id` and `causation_id` so clients can reconstruct why an event happened, not only when it happened.

### 6.2 Message Types

**Control Messages**

- `session.open`
- `session.accepted`
- `session.close`
- `ping`
- `pong`
- `ack`
- `nack`
- `cancel`
- `resume`
- `backpressure`
- `checkpoint.create`
- `checkpoint.restore`
- `permission.request`
- `permission.grant`
- `permission.deny`

**Execution Messages**

- `tool.invoke`
- `tool.result`
- `tool.error`
- `job.accepted`
- `job.started`
- `job.progress`
- `job.heartbeat`
- `job.checkpoint`
- `job.completed`
- `job.failed`
- `job.cancelled`
- `workflow.start`
- `workflow.complete`
- `agent.delegate`
- `agent.handoff`

**Streaming Messages**

- `stream.open`
- `stream.chunk`
- `stream.close`
- `stream.error`

**Event Messages**

- `event.emit`
- `log`
- `metric`
- `trace.span`

### 6.3 Command, Result, and Event Flow

ARCP does not require commands to complete synchronously. A command **MAY** be acknowledged immediately, then produce job, stream, log, metric, and trace events over time.

Common flow:

1. Client sends a command, such as `workflow.start` or `tool.invoke`.
2. Runtime returns `ack` or `job.accepted` with `correlation_id` set to the command id.
3. Runtime emits `job.started` when execution begins.
4. Runtime emits `stream.chunk`, `job.progress`, `log`, `metric`, and `job.checkpoint` events.
5. Runtime emits exactly one terminal event. Direct tool invocations terminate with `tool.result` or `tool.error`. Durable jobs terminate with `job.completed`, `job.failed`, or `job.cancelled`. Workflow-only invocations **MAY** terminate with `workflow.complete`.

If a runtime cannot accept the command, it **MUST** return `nack` or a structured error event with `correlation_id` set to the rejected command id.

### 6.4 Delivery Semantics

ARCP implementations **SHOULD** support at-least-once delivery for durable jobs. Because messages can be replayed after reconnects, receivers **MUST** deduplicate by `id` and **SHOULD** make tool execution idempotent with explicit operation keys in the payload.

Ordering is guaranteed only within a `stream_id` or `job_id` unless the transport provides stronger ordering. Clients **SHOULD** use `timestamp`, `correlation_id`, and `causation_id` to rebuild the execution graph.

---

## 7. Capability Negotiation

Clients and runtimes **MUST** negotiate capabilities during session establishment.

Example:

```json
{
  "capabilities": {
    "streaming": true,
    "durable_jobs": true,
    "checkpoints": true,
    "binary_streams": false,
    "agent_handoff": true
  }
}
```

---

## 8. Sessions

Sessions **MAY** be:

- stateless
- stateful
- durable

Stateful sessions **MAY**:

- maintain memory
- preserve auth
- cache resources
- share execution context

---

## 9. Jobs

### 9.1 Durable Jobs

Jobs **MUST** support:

- retries
- heartbeats
- checkpoints
- cancellation
- progress reporting

Example:

```json
{
  "type": "job.progress",
  "payload": {
    "percent": 42,
    "message": "Embedding documents"
  }
}
```

### 9.2 Job States

| State | Description |
|-------|-------------|
| `accepted` | Runtime accepted the command but has not started work |
| `queued` | Work is waiting for capacity, permissions, or dependencies |
| `running` | Work is actively executing |
| `blocked` | Work is waiting on an external event, permission, or human input |
| `paused` | Work was intentionally suspended and can be resumed |
| `completed` | Work finished successfully |
| `failed` | Work reached a terminal error |
| `cancelled` | Work was cancelled by a client, runtime, policy, or timeout |

Each job **MUST** emit one terminal state. Durable runtimes **SHOULD** persist the last known state, latest checkpoint, retry count, and cancellation reason.

---

## 10. Streaming

Streams support:

- text
- binary
- structured events
- logs
- telemetry

Streams **MAY** be multiplexed.

Streams **MUST** support backpressure signaling.

### 10.1 Backpressure

Clients and runtimes **MAY** send backpressure messages when they cannot process a stream at the current rate.

Example:

```json
{
  "type": "backpressure",
  "stream_id": "str_123",
  "payload": {
    "desired_rate_per_second": 20,
    "buffer_remaining_bytes": 65536,
    "reason": "client_render_queue_full"
  }
}
```

Senders **SHOULD** slow or batch `stream.chunk` events after receiving backpressure.

---

## 11. Multi-Agent Coordination

ARCP defines optional primitives for:

- agent discovery
- delegation
- handoff
- shared context
- distributed workflows

Example:

```json
{
  "type": "agent.delegate",
  "payload": {
    "target": "research-agent",
    "task": "Summarize RFCs"
  }
}
```

---

## 12. Permissions & Security

### 12.1 Permission Model

Permissions **MUST** be explicit.

Examples:

- `filesystem.read`
- `filesystem.write`
- `network.fetch`
- `email.send`
- `shell.execute`

### 12.2 Sandboxing

Runtimes **SHOULD**:

- isolate execution
- restrict network access
- enforce capability boundaries

### 12.3 Trust Levels

ARCP defines trust classifications:

| Level | Description |
|-------|-------------|
| `untrusted` | External/public |
| `constrained` | Limited access |
| `trusted` | Internal |
| `privileged` | System-level |

### 12.4 Permission Challenge Flow

Permissioned operations **SHOULD** use a challenge/response flow:

1. Runtime detects an operation that requires a permission not already covered by the session.
2. Runtime emits `permission.request` and moves the job to `blocked`.
3. Client responds with `permission.grant` or `permission.deny`.
4. Runtime resumes, fails, or delegates according to policy.

Permission grants **SHOULD** be scoped to a specific lease, resource, operation, and expiration time.

Example:

```json
{
  "type": "permission.request",
  "job_id": "job_refund_123",
  "payload": {
    "permission": "payment.refund.create",
    "resource": "order:ord_4812",
    "operation": "refund",
    "reason": "Issue a customer-approved refund",
    "requested_lease_seconds": 300
  }
}
```

---

## 13. Observability

ARCP includes native observability primitives.

### 13.1 Tracing

All messages **SHOULD** include:

- `trace_id`
- `span_id`

Compatible with:

- OpenTelemetry
- Datadog
- Honeycomb

### 13.2 Structured Logs

Example:

```json
{
  "type": "log",
  "payload": {
    "level": "warn",
    "message": "Retrying tool invocation"
  }
}
```

---

## 14. Error Model

Errors **MUST** be structured.

Example:

```json
{
  "type": "tool.error",
  "payload": {
    "code": "RATE_LIMITED",
    "retryable": true,
    "message": "Upstream rate limit exceeded"
  }
}
```

---

## 15. Resumability

ARCP supports:

- checkpoint snapshots
- replay
- recovery
- stream resumption

Clients **MAY** reconnect and resume execution.

Resume requests **SHOULD** identify the last message id or checkpoint observed by the client.

Example:

```json
{
  "type": "resume",
  "session_id": "sess_123",
  "job_id": "job_456",
  "payload": {
    "after_message_id": "msg_01JABC",
    "checkpoint_id": "chk_007",
    "include_open_streams": true
  }
}
```

---

## 16. MCP Compatibility

ARCP **MAY** wrap MCP servers.

Example mapping:

| MCP | ARCP |
|-----|------|
| tool schema | capability |
| tool call | job |
| resource | stream/resource |
| prompt | invocation payload |

---

## 17. Reference Transports

**Mandatory**

- WebSocket
- stdio

**Recommended**

- HTTP/2
- QUIC

---

## 18. Example Lifecycle

1. Open session
2. Negotiate capabilities
3. Invoke tool
4. Open stream
5. Emit progress
6. Emit checkpoints
7. Complete job
8. Persist trace
9. Close session

---

## 19. Example Invocation

```json
{
  "type": "tool.invoke",
  "payload": {
    "tool": "filesystem.search",
    "arguments": {
      "query": "*.ts"
    }
  }
}
```

---

## 20. Real-World Examples

Concrete examples are included in:

- [docs/real-world-examples.md](real-world-examples.md)
- [examples/customer-support-refund.jsonl](../examples/customer-support-refund.jsonl)
- [examples/local-code-review.jsonl](../examples/local-code-review.jsonl)
- [examples/data-ingestion-workflow.jsonl](../examples/data-ingestion-workflow.jsonl)
- [examples/incident-response.jsonl](../examples/incident-response.jsonl)

These examples show how ARCP behaves in common production settings:

- A support copilot that looks up an order, requests a scoped refund permission, and streams customer-visible status.
- A local development agent that reviews code, requests write access, patches files, and streams test output.
- A durable ingestion workflow that checkpoints progress, handles retryable errors, and resumes after failure.
- A multi-agent incident workflow that delegates work, preserves shared trace context, and requests approval before rollback.

The examples are intentionally transport-neutral. The same envelopes can move over stdio, WebSocket, HTTP/2, QUIC, or a message queue as long as the transport preserves the message body and delivery contract.

---

## 21. Future Work

Potential extensions:

- CRDT-based shared state
- Real-time collaborative agents
- WASM execution sandboxes
- GPU scheduling
- Federated runtime mesh
- Signed capability manifests
- Economic metering/billing
- Agent marketplaces

---

## 22. Why ARCP Exists

Current ecosystems lack a unified runtime protocol for:

- durable execution
- orchestration
- structured streams
- secure delegation
- observable agent execution

ARCP provides:

- execution semantics
- lifecycle management
- runtime interoperability

while remaining compatible with:

- MCP
- JSON-RPC
- OpenAI tools
- Anthropic tools
- future agent ecosystems

---

## 23. Reference Motto

**MCP** describes capabilities.  
**ARCP** operationalizes them.
