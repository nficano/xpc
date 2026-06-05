package otel

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"

	otlploggrpc "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	otlpmetricgrpc "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
)

const scopeName = "github.com/nficano/xpc/internal/otel"

// Provider holds the OTel SDK state for one VM's export pipeline. A Provider
// exports metrics from the latest Snapshot set via SetSnapshot (read by an
// observable-instrument callback on the metric reader's cadence) and logs
// pushed eagerly via EmitLogs.
type Provider struct {
	cfg Config

	mp *sdkmetric.MeterProvider
	lp *sdklog.LoggerProvider

	logger otellog.Logger

	// Observable instruments, registered once; the callback reads snap.
	cpu       metric.Float64ObservableGauge
	memAvail  metric.Int64ObservableGauge
	procMem   metric.Int64ObservableGauge
	procCount metric.Int64ObservableGauge
	svcUp     metric.Int64ObservableGauge
	reachable metric.Int64ObservableGauge
	scrapeDur metric.Float64ObservableGauge

	mu   sync.RWMutex
	snap *Snapshot
}

// New builds a Provider for cfg. The OTLP gRPC exporters dial lazily, so New
// succeeds even when the collector is unreachable; exports retry in the
// background. Callers must Shutdown the returned Provider to flush.
func New(ctx context.Context, cfg Config) (*Provider, error) {
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Second
	}
	res, err := resource.New(ctx, resource.WithAttributes(
		attribute.String("service.namespace", "xpc"),
		attribute.String("service.name", "xpc-otel"),
		attribute.String("host.name", cfg.Profile),
		attribute.String("host.id", cfg.Profile),
		attribute.String("xpc.profile", cfg.Profile),
		attribute.String("xpc.vm.endpoint", cfg.VMEndpoint),
		attribute.String("xpc.collector.host", cfg.CollectorHost),
		attribute.String("os.type", "windows"),
	))
	if err != nil {
		return nil, fmt.Errorf("otel: build resource: %w", err)
	}

	p := &Provider{cfg: cfg}

	if cfg.Signals.Metrics {
		if err := p.initMetrics(ctx, res); err != nil {
			return nil, err
		}
	}
	if cfg.Signals.Logs {
		if err := p.initLogs(ctx, res); err != nil {
			_ = p.Shutdown(ctx)
			return nil, err
		}
	}
	return p, nil
}

func (p *Provider) initMetrics(ctx context.Context, res *resource.Resource) error {
	opts := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpoint(p.cfg.Endpoint)}
	if p.cfg.Insecure {
		opts = append(opts, otlpmetricgrpc.WithInsecure())
	}
	if len(p.cfg.Headers) > 0 {
		opts = append(opts, otlpmetricgrpc.WithHeaders(p.cfg.Headers))
	}
	exp, err := otlpmetricgrpc.New(ctx, opts...)
	if err != nil {
		return fmt.Errorf("otel: metric exporter: %w", err)
	}
	reader := sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(p.cfg.Interval))
	p.mp = sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(reader),
	)

	m := p.mp.Meter(scopeName)
	if p.cpu, err = m.Float64ObservableGauge("host.cpu.utilization",
		metric.WithDescription("Total processor utilization (0..1)."),
		metric.WithUnit("1")); err != nil {
		return err
	}
	if p.memAvail, err = m.Int64ObservableGauge("host.memory.available",
		metric.WithDescription("Available physical memory."),
		metric.WithUnit("By")); err != nil {
		return err
	}
	if p.procMem, err = m.Int64ObservableGauge("process.memory.usage",
		metric.WithDescription("Per-process working-set memory."),
		metric.WithUnit("By")); err != nil {
		return err
	}
	if p.procCount, err = m.Int64ObservableGauge("process.count",
		metric.WithDescription("Number of running processes.")); err != nil {
		return err
	}
	if p.svcUp, err = m.Int64ObservableGauge("service.up",
		metric.WithDescription("Service running state (1=running, 0=not).")); err != nil {
		return err
	}
	if p.reachable, err = m.Int64ObservableGauge("xpc.vm.reachable",
		metric.WithDescription("Whether the last scrape reached the VM (1/0).")); err != nil {
		return err
	}
	if p.scrapeDur, err = m.Float64ObservableGauge("xpc.scrape.duration",
		metric.WithDescription("Wall-clock duration of the last scrape."),
		metric.WithUnit("s")); err != nil {
		return err
	}

	_, err = m.RegisterCallback(p.observe,
		p.cpu, p.memAvail, p.procMem, p.procCount, p.svcUp, p.reachable, p.scrapeDur)
	return err
}

func (p *Provider) initLogs(ctx context.Context, res *resource.Resource) error {
	opts := []otlploggrpc.Option{otlploggrpc.WithEndpoint(p.cfg.Endpoint)}
	if p.cfg.Insecure {
		opts = append(opts, otlploggrpc.WithInsecure())
	}
	if len(p.cfg.Headers) > 0 {
		opts = append(opts, otlploggrpc.WithHeaders(p.cfg.Headers))
	}
	exp, err := otlploggrpc.New(ctx, opts...)
	if err != nil {
		return fmt.Errorf("otel: log exporter: %w", err)
	}
	p.lp = sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)),
	)
	p.logger = p.lp.Logger(scopeName)
	return nil
}

// SetSnapshot stores the latest metric snapshot. The metric reader's callback
// reads it on the next collection; it is safe to call concurrently.
func (p *Provider) SetSnapshot(s *Snapshot) {
	p.mu.Lock()
	p.snap = s
	p.mu.Unlock()
}

// observe is the single multi-instrument callback. Using asynchronous
// instruments means series for processes/services that vanish between scrapes
// are simply not re-observed (no stale-series accumulation).
func (p *Provider) observe(_ context.Context, o metric.Observer) error {
	p.mu.RLock()
	snap := p.snap
	p.mu.RUnlock()
	if snap == nil {
		return nil
	}
	if snap.CPUUtil != nil {
		o.ObserveFloat64(p.cpu, *snap.CPUUtil)
	}
	if snap.MemAvailable != nil {
		o.ObserveInt64(p.memAvail, *snap.MemAvailable)
	}
	o.ObserveInt64(p.procCount, int64(snap.ProcessCount))
	for _, pr := range snap.Processes {
		o.ObserveInt64(p.procMem, pr.MemBytes, metric.WithAttributes(
			attribute.String("process.name", pr.Name),
			attribute.Int("process.pid", pr.PID),
		))
	}
	for _, s := range snap.Services {
		o.ObserveInt64(p.svcUp, boolToInt(s.Up), metric.WithAttributes(
			attribute.String("service.name", s.Name),
		))
	}
	o.ObserveInt64(p.reachable, boolToInt(snap.Reachable))
	o.ObserveFloat64(p.scrapeDur, snap.ScrapeSeconds)
	return nil
}

// EmitLogs maps Windows Event Log entries to OTLP LogRecords and emits them.
// No-op when log export is disabled.
func (p *Provider) EmitLogs(ctx context.Context, entries []LogEntry) {
	if p.logger == nil {
		return
	}
	now := time.Now()
	for _, e := range entries {
		var r otellog.Record
		// eventquery.vbs reports the event time in the HOST's local clock with no
		// timezone, and the collector doesn't know the VM's locale -- parsing it as
		// the collector's tz mis-places records (a Pacific host scraped by a UTC
		// collector lands ~7h in the past, outside normal time windows). This is a
		// near-real-time tailer with no backfill, so use the scrape time as the
		// record timestamp and keep the host-local clock string as an attribute.
		r.SetTimestamp(now)
		r.SetObservedTimestamp(now)
		sev, txt := severityFor(e.Type)
		r.SetSeverity(sev)
		r.SetSeverityText(txt)
		r.SetBody(otellog.StringValue(e.Body))
		attrs := []otellog.KeyValue{otellog.String("log.name", e.LogName)}
		if e.EventID != "" {
			attrs = append(attrs, otellog.String("event.id", e.EventID))
		}
		if e.Source != "" {
			attrs = append(attrs, otellog.String("event.source", e.Source))
		}
		if e.Category != "" {
			attrs = append(attrs, otellog.String("event.category", e.Category))
		}
		if !e.Timestamp.IsZero() {
			attrs = append(attrs, otellog.String("event.time", e.Timestamp.Format("2006-01-02 15:04:05")))
		}
		r.AddAttributes(attrs...)
		p.logger.Emit(ctx, r)
	}
}

// ForceFlush pushes any buffered metrics and logs immediately.
func (p *Provider) ForceFlush(ctx context.Context) error {
	var firstErr error
	if p.mp != nil {
		if err := p.mp.ForceFlush(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if p.lp != nil {
		if err := p.lp.ForceFlush(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Shutdown flushes and tears down both providers. Safe to call once.
func (p *Provider) Shutdown(ctx context.Context) error {
	var firstErr error
	if p.mp != nil {
		if err := p.mp.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
		p.mp = nil
	}
	if p.lp != nil {
		if err := p.lp.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
		p.lp = nil
	}
	return firstErr
}

// severityFor maps a Windows Event Log type token to an OTLP severity.
func severityFor(t string) (otellog.Severity, string) {
	// Case-insensitive: eventquery.vbs emits lower-case types ("information") on
	// some XP builds. Preserve the canonical descriptive label in severity_text.
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "error":
		return otellog.SeverityError, "Error"
	case "failure audit":
		return otellog.SeverityError, "Failure Audit"
	case "warning":
		return otellog.SeverityWarn, "Warning"
	case "information":
		return otellog.SeverityInfo, "Information"
	case "success audit":
		return otellog.SeverityInfo, "Success Audit"
	case "":
		return otellog.SeverityUndefined, ""
	default:
		return otellog.SeverityInfo, strings.TrimSpace(t)
	}
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
