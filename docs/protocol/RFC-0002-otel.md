# RFC 0002 — OpenTelemetry Export for xpc

**Status:** Draft

**Authors:** Nick Ficano et al.

**Supersedes:** none

**Depends on:** [RFC 0001 — ARCP](RFC-0001.md)

## Abstract

This RFC specifies how `xpc` collects telemetry from managed Windows XP virtual
machines and exports it as [OpenTelemetry](https://opentelemetry.io) (OTel)
signals. It fills a slot that the project reserved from the start:
[RFC 0001](RFC-0001.md) defines the `metric` and `trace.span` event types but
leaves them unimplemented, and `docs/ARCHITECTURE.md` records the decision as
*"Event: `log` only. **Deferred:** `metric`, `trace.span` (use OTel later)."*
This is the "later."

The central design choice is that **xpc is an OTLP *producer*, not a telemetry
*backend integrator*.** The host-side `xpc` process scrapes each VM over ARCP,
translates the results into OTLP metrics and logs, and pushes them to a standard
OpenTelemetry Collector. The Collector — not xpc — owns batching, retry,
fan-out, and backend selection (Prometheus, Loki, Tempo, Datadog, Honeycomb,
vendor OTLP, …).

ARCP already lists OpenTelemetry, Datadog, and Honeycomb as compatible
observability backends ([RFC 0001 §13.1](RFC-0001.md)). This document makes that
compatibility concrete for the xpc/Windows XP environment.

---

## 1. Goals

### 1.1 Primary Goals

- Export host and process **metrics** from managed XP VMs as OTLP.
- Export Windows **Event Log** records from managed XP VMs as OTLP logs.
- Run unattended as a daemonized, server-side collector over a fleet of VMs.
- Reuse the existing ARCP transport, profiles, and warm-session machinery
  rather than introducing a second control plane.
- Emit OTLP to any standards-compliant OTel Collector endpoint, leaving
  backend choice entirely downstream.
- Be packageable as a single container image for production deployment.

### 1.2 Secondary Goals

- Optionally emit **traces** for xpc's *own* control-plane operations (one span
  per scrape / remote command) to debug the management layer.
- Provide a low-round-trip agent-side `collect` operation that gathers all
  per-tick counters in a single ARCP invocation.

---

## 2. Non-Goals

This RFC intentionally does **not** define:

- A telemetry storage engine, query language, or dashboard.
- Backend-specific exporters inside xpc (Prometheus remote-write, Datadog API,
  Loki push, …). These belong in the Collector.
- A Prometheus *scrape* endpoint served by xpc. xpc is push-only (OTLP); if a
  pull model is required, the Collector exposes it.
- Application-level distributed tracing *inside* software running on the XP VM.
  xpc observes VMs externally; it does not instrument guest applications.
- Running an OpenTelemetry Collector or OTel SDK *on* the XP VM. See §4.

xpc **MAY** interoperate with all of the above through the Collector.

---

## 3. Terminology

| Term | Definition |
|------|------------|
| Producer | A process that originates OTLP signals. In this RFC, the host-side `xpc` collector mode. |
| Collector | A standard OpenTelemetry Collector that receives OTLP and routes it to backends. |
| Scrape | One polling cycle against one VM that yields a set of metrics and/or logs. |
| Signal | An OTel metric, log, or trace. |
| Resource | The OTel entity a signal is attributed to — here, a specific XP VM. |
| Profile | An xpc connection profile (`internal/profile`) identifying one VM + PSK + endpoint. |
| Agent | The Python 3.4 on-VM process defined by [RFC 0001](RFC-0001.md). |

The key words **MUST**, **MUST NOT**, **SHOULD**, **SHOULD NOT**, and **MAY**
are to be interpreted as described in RFC 2119.

---

## 4. The Decisive Constraint

The on-VM agent is **Python 3.4 on Windows XP** ([RFC 0001](RFC-0001.md);
`agent/agent.py`). This single fact determines the entire architecture:

- The OpenTelemetry Collector does not run on Windows XP.
- The OpenTelemetry Python SDK requires Python 3.8+; it does not run on 3.4.
- No modern OTLP-emitting runtime targets XP.

Therefore **OTLP encoding MUST happen host-side, in the Go process**, where the
official OpenTelemetry Go SDK and OTLP exporters are available and where xpc
already parses command output into typed structures (for example the
`Process` struct in `internal/cli/ps.go`).

The agent's role is unchanged: it executes commands and returns structured
results. It does **not** gain OTel awareness. This preserves the project's
existing "host orchestrates, agent executes" model and keeps the XP footprint
minimal.

A corollary: any approach that bypasses the agent (for example, scraping the XP
SNMP service, or tailing guest files from off-box) cannot reach the process
table, service states, or Event Log with the fidelity the agent already
provides. Such sources are **complementary**, not substitutes (see §16).

---

## 5. Architecture

```text
   Windows XP VM                 Host (Go)                    OTel Collector            Backends
+------------------+         +-------------------+         +------------------+      +-----------+
| python 3.4 agent |  ARCP   | xpc otel export   |  OTLP   | receivers:       |      | Prometheus|
|  tasklist /v     | <-TLS-> |  - poll interval  | -gRPC-> |   otlp           | ---> | Loki      |
|  typeperf ...    |  HMAC   |  - parse -> OTLP   |  or     | processors:      |      | Tempo     |
|  sc query        |         |  - resource attrs |  HTTP   |   batch, attrs   |      | Datadog   |
|  eventquery.vbs  |         |  per VM           |         | exporters:       |      | vendor    |
+------------------+         +-------------------+         |   (any backend)  |      +-----------+
                                                           +------------------+
                                       (optional additional receivers: statsd / snmp / filelog)
```

The new layer sits beside the existing CLI dispatch layer and consumes the same
ARCP runtime layer described in [RFC 0001 §5](RFC-0001.md):

```text
+-----------------------------+
| OTel Export Layer (this RFC)|   host-side: scrape -> OTLP producer
+-----------------------------+
+-----------------------------+
| ARCP Runtime Layer          |   sessions, streams, tool.invoke (RFC 0001)
+-----------------------------+
+-----------------------------+
| Transport Layer (TLS+HMAC)  |
+-----------------------------+
```

---

## 6. Position of xpc: Producer, Not Integrator

xpc's responsibility **MUST** stay narrow:

1. Scrape VMs it already knows how to reach (by profile).
2. Translate parsed results into OTLP metrics and logs.
3. Push OTLP to a configurable endpoint.

xpc **MUST NOT** implement backend-specific export. The OpenTelemetry Collector
is the integration point; it owns batching, retry/queueing, redaction, sampling,
and routing to one or more backends. This keeps xpc small and lets operators
change backends without touching xpc.

---

## 7. Signal Model

### 7.1 Metrics

Metrics are the primary signal. Sources are `typeperf` (real Windows
performance counters), `tasklist` (per-process), and `sc query` / `net start`
(services).

| Instrument | Type | Unit | Source |
|------------|------|------|--------|
| `host.cpu.utilization` | Gauge | `1` (ratio) | `typeperf "\Processor(_Total)\% Processor Time"` |
| `host.memory.available` | Gauge | `By` | `typeperf "\Memory\Available Bytes"` |
| `host.memory.committed` | Gauge | `By` | `typeperf "\Memory\Committed Bytes"` |
| `host.disk.io.time` | Gauge | `1` | `typeperf "\PhysicalDisk(_Total)\% Disk Time"` |
| `host.network.bytes` | Sum | `By` | `typeperf "\Network Interface(*)\Bytes Total/sec"` |
| `process.memory.usage` | Gauge | `By` | `tasklist /v /fo csv` (already parsed) |
| `process.count` | Gauge | `1` | `tasklist` row count |
| `service.up` | Gauge | `1`/`0` | `sc query <name>` state |
| `xpc.scrape.duration` | Histogram | `s` | host-side timing |
| `xpc.vm.reachable` | Gauge | `1`/`0` | session success/failure |

Metric and attribute names **SHOULD** follow OpenTelemetry semantic conventions
where one exists, and use the `host.*`, `process.*`, and `service.*` namespaces
otherwise.

### 7.2 Logs

Windows **Event Log** records (Application, System, Security, …) map cleanly to
OTLP `LogRecord`s. The extraction already exists in `internal/cli/evt.go`
(`eventquery.vbs` wrapper).

| Event Log field | OTLP LogRecord field |
|-----------------|----------------------|
| Type (Error / Warning / Information / …) | `SeverityNumber` + `SeverityText` |
| Description / Message | `Body` |
| Event ID | attribute `event.id` |
| Source | attribute `event.source` |
| Time Generated | `Timestamp` |
| Category | attribute `event.category` |

Severity mapping **SHOULD** be:

| Event Log Type | SeverityNumber |
|----------------|----------------|
| Error / Failure Audit | `ERROR` (17) |
| Warning | `WARN` (13) |
| Information / Success Audit | `INFO` (9) |

Each scrape **SHOULD** track the last-seen record per log so repeated scrapes do
not re-emit the same records. The high-water mark **MAY** be persisted under
`~/.xpc/run/`.

### 7.3 Traces

For an externally observed XP box, guest-application traces are out of scope
(§2). The supported use is instrumenting **xpc's own control plane**: one span
per scrape cycle, with child spans per remote command. This realizes the
`trace.span` event type reserved in [RFC 0001 §6.2](RFC-0001.md) and is
**OPTIONAL** in this RFC's first phase.

---

## 8. Resource Semantic Conventions

Every signal **MUST** carry a Resource that identifies the originating VM, so a
fleet is distinguishable in the backend.

| Resource attribute | Value |
|--------------------|-------|
| `service.namespace` | `xpc` |
| `service.name` | `xpc-otel` |
| `host.name` | profile name |
| `host.id` | profile name (stable id) |
| `xpc.profile` | profile name |
| `xpc.vm.endpoint` | profile host/IP:port |
| `os.type` | `windows` |
| `os.version` | from `systeminfo` (cached per session) |

The host running `xpc` itself **SHOULD** be recorded as
`xpc.collector.host` so multiple collectors over one fleet are attributable.

---

## 9. Data Sources and the Agent `collect` Operation

### 9.1 v1: reuse existing commands

The first implementation **SHOULD** reuse `runRemoteCmd`
(`internal/cli/run.go`) to issue the same commands the CLI already runs
(`tasklist`, `typeperf`, `sc query`, `eventquery.vbs`) and translate the parsed
output. No agent change is required.

### 9.2 Optimization: a single `collect` invocation

Issuing several commands per VM per tick costs several ARCP round trips. A later
optimization **MAY** add an agent-side `collect` operation that runs `typeperf`,
`tasklist`, and `sc query` once and returns a single JSON document:

```json
{
  "ts": "2026-06-03T18:00:00Z",
  "cpu_total_pct": 7.4,
  "mem_available_bytes": 268435456,
  "processes": [{"name": "explorer.exe", "pid": 1234, "mem_kb": 20480}],
  "services": [{"name": "Spooler", "state": "RUNNING"}]
}
```

This is pure Python standard library (`subprocess` + `json`) and runs on 3.4. It
reduces a multi-command scrape to one `tool.invoke` / `tool.result` exchange.

---

## 10. ARCP Integration

This RFC does **not** require new ARCP wire types for the v1 host-side scrape
model: the host invokes existing tools (`exec`, or a new `collect` tool) and
translates results locally.

When agent-originated telemetry is desired, it **SHOULD** use the `metric` and
`trace.span` event types already reserved in [RFC 0001 §6.2](RFC-0001.md) and
[`docs/PROTOCOL.md` §11](../PROTOCOL.md). The envelope shape already
accommodates them; only dispatch handlers would be added. Such events **MUST**
remain a transport for raw measurements — OTLP encoding still happens host-side
per §4.

---

## 11. Command Surface

A new long-lived command exposes the collector:

```
xpc otel export [flags]

  --profile <name>         VM profile to scrape (repeatable; default: current)
  --endpoint <host:port>   OTLP gRPC endpoint of the Collector (default :4317)
  --protocol grpc|http     OTLP transport (default grpc)
  --interval <dur>         Scrape interval (default 30s)
  --insecure               Disable TLS to the Collector (lab only)
  --signals metrics,logs   Which signals to export (default metrics,logs)
  --config <path>          Load scrape config from a file (overrides flags)
```

Behavior:

- Runs in the foreground; `&` or a service wrapper daemonizes it. It **SHOULD**
  honor `SIGTERM` for clean shutdown and flush pending OTLP on exit.
- It **SHOULD** hold a warm ARCP session per profile (see §13) and reconnect on
  failure, emitting `xpc.vm.reachable=0` for the affected scrape.
- A non-fatal scrape error for one VM **MUST NOT** stop scraping of other VMs.

The command lives in `internal/cli/otel.go`; OTLP plumbing lives in a new
`internal/otel/` package.

---

## 12. Configuration

For multi-VM deployments, a config file is preferred over flags:

```yaml
# ~/.xpc/otel.yaml
exporter:
  endpoint: collector.internal:4317
  protocol: grpc
  insecure: false
defaults:
  interval: 30s
  signals: [metrics, logs]
  event_logs: [Application, System]
targets:
  - profile: lab-xp-01
  - profile: lab-xp-02
    interval: 15s
  - profile: prod-xp-fleet-03
    signals: [logs]
```

Resolution order **SHOULD** be: command-line flags > `--config` file >
`~/.xpc/otel.yaml` > built-in defaults.

---

## 13. Reuse of Existing Components

The collector mode is built from machinery that already exists:

| Need | Existing component |
|------|--------------------|
| Warm per-profile ARCP sessions | `internal/cli/daemon.go` (`daemon.session`, `dialAndOpen`) |
| Interval polling loop | `internal/cli/watch.go` |
| One-shot remote command + parse | `internal/cli/run.go` (`runRemoteCmd`), `internal/cli/ps.go`, `evt.go`, `svc.go` |
| Profiles / PSK / endpoints | `internal/profile` |

The collector loop **SHOULD** borrow the daemon's session pool so a fleet scrape
pays the TLS+session handshake once per profile, not once per tick.

---

## 14. Security

- The ARCP leg keeps RFC 0001's TLS + HMAC guarantees; no new trust surface is
  introduced toward the VM.
- The OTLP leg **SHOULD** use TLS to the Collector in production. `--insecure`
  is for lab use only and **SHOULD** be logged as a warning at startup.
- Telemetry can carry sensitive values (process command lines, Event Log
  message bodies). Redaction/scrubbing **SHOULD** be performed in the Collector
  (an `attributes`/`transform` processor), keeping xpc free of policy.
- Event Log **Security** channel export **SHOULD** be opt-in.

---

## 15. Deployment

The same `xpc` binary in `otel export` mode is the deployable unit. For
unattended server-side collection it **SHOULD** be shipped as a container:

```dockerfile
# illustrative
FROM gcr.io/distroless/static
COPY xpc /usr/local/bin/xpc
COPY otel.yaml /etc/xpc/otel.yaml
ENTRYPOINT ["xpc", "otel", "export", "--config", "/etc/xpc/otel.yaml"]
```

This satisfies the "containerized collection service" requirement without a
second codebase: it is the xpc binary in a run mode, not a separate service that
shells out to xpc (the `internal/` packages are not importable, and shelling out
is brittle).

---

## 16. Relationship to statsd / SNMP / filelog

These are **downstream receivers on the Collector**, not replacements for the
xpc path:

- They cannot reach XP's process table, service states, or Event Log with the
  agent's fidelity (§4).
- The XP SNMP service is legacy and exposes a thin slice of counters.
- `filelog` requires off-box file access (a share); `statsd` requires a guest
  process emitting statsd (none exists by default).

If an operator already has such sources, the Collector **MAY** add the
corresponding receivers alongside the `otlp` receiver fed by xpc. They augment
coverage; they do not displace the agent-driven scrape.

---

## 17. Phased Rollout

**Phase 1 — minimal useful export.**

1. `xpc otel export --profile <p> --endpoint :4317 --interval 30s`.
2. Metrics: `host.cpu.utilization`, `host.memory.available` (typeperf),
   per-process memory (tasklist), `service.up`.
3. Logs: Application + System Event Log → OTLP logs with severity mapping.
4. Per-VM Resource attributes; OTLP/gRPC push.
5. Verify against a local `otelcol` using the `debug` exporter, then
   containerize.

**Phase 2 — fleet + ergonomics.**

- `--config` / `~/.xpc/otel.yaml`, multi-profile scraping, warm-session reuse.
- Event Log high-water marks; OTLP/HTTP option; TLS to Collector.

**Phase 3 — efficiency + self-observability.**

- Agent-side `collect` operation (§9.2).
- Control-plane traces (§7.3) via the reserved `trace.span` type.

---

## 18. Future Work

- Network and disk throughput counters beyond Phase 1.
- Optional Prometheus exposition via the Collector (documentation only; xpc
  stays push-only).
- Agent-originated `metric` events for guest software that *can* emit them.
- Service-install integration so `xpc otel export` can register as a host
  service (`xpc svc install`).

---

## 19. References

- [RFC 0001 — ARCP](RFC-0001.md) — envelope, sessions, reserved `metric` /
  `trace.span` event types, observability primitives.
- [`docs/PROTOCOL.md`](../PROTOCOL.md) — xpc wire protocol; §11 reserved types.
- [`docs/ARCHITECTURE.md`](../ARCHITECTURE.md) — records the OTel deferral this
  RFC resolves.
- OpenTelemetry Protocol (OTLP) specification.
- OpenTelemetry semantic conventions for host, process, and log signals.
