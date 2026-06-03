# Local OTLP collector for `xpc otel export`

A one-command [OpenTelemetry Collector](https://opentelemetry.io/docs/collector/)
that receives OTLP from `xpc otel export` and prints every metric and log to its
stdout. Use it to verify the export path end to end before pointing xpc at a
real backend. See [RFC 0002](../../docs/protocol/RFC-0002-otel.md).

## Run

```bash
# 1. start the collector (OTLP gRPC :4317, HTTP :4318)
docker compose -f deploy/otel/docker-compose.yml up -d

# 2. export from a configured VM profile to it
xpc otel export --endpoint localhost:4317 --insecure --interval 15s \
  --services Spooler,Themes

# 3. watch what xpc is sending
docker compose -f deploy/otel/docker-compose.yml logs -f otelcol
```

`--insecure` is required here because the local collector serves plaintext
OTLP. In production, terminate TLS at the collector and drop `--insecure`.

## Going to production

Swap the `debug` exporter in [otel-collector.yaml](otel-collector.yaml) for a
real backend (Prometheus, Loki, Tempo, or a vendor's OTLP endpoint). That
choice lives in the collector, not in xpc — xpc only ever speaks OTLP. The same
file shows where complementary `statsd`/`filelog` receivers plug in (RFC 0002
§16).
