package cli

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/nficano/xpc/internal/otel"
)

// xpc otel
//
// Host-side OpenTelemetry export of VM telemetry. A long-lived process scrapes
// the VM over ARCP on an interval, maps the results to OTLP metrics and logs,
// and pushes them to an OpenTelemetry Collector. Per RFC 0002, OTLP encoding
// lives here (Go), not on the Python 3.4 agent.
//
//	xpc otel export --endpoint collector:4317 --interval 30s
//
// Phase 1 (this command): single profile, OTLP/gRPC push, host + per-process
// metrics, service up/down, and Application/System Event Log -> OTLP logs.

func newOtelCmd(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "otel",
		Short: "OpenTelemetry export of VM telemetry (RFC 0002).",
	}
	cmd.AddCommand(newOtelExportCmd(g))
	return cmd
}

func newOtelExportCmd(g *Globals) *cobra.Command {
	var (
		endpoint  string
		protocol  string
		interval  time.Duration
		insecure  bool
		signals   string
		services  []string
		eventLogs []string
		eventMax  int
	)
	c := &cobra.Command{
		Use:   "export",
		Short: "Scrape the VM on an interval and push OTLP metrics + logs.",
		Long: `Runs in the foreground; background it with & or a service wrapper, and
stop it with SIGINT/SIGTERM (pending telemetry is flushed on exit).

It scrapes the profile's VM every --interval and exports:
  metrics  host CPU/memory (typeperf), per-process memory (tasklist),
           service up/down (--services), plus xpc.vm.reachable.
  logs     Windows Event Log records (--event-logs) as OTLP logs.

OTLP is pushed over gRPC to --endpoint, which should be an OpenTelemetry
Collector. Choice of backend (Prometheus, Loki, Tempo, vendor, ...) is the
Collector's job, not xpc's.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if protocol != "grpc" {
				return wrapUsage(fmt.Errorf("--protocol %q: only grpc is supported in Phase 1 (HTTP is Phase 2)", protocol))
			}
			sig, err := parseSignals(signals)
			if err != nil {
				return wrapUsage(err)
			}
			p, err := g.ResolveProfile()
			if err != nil {
				return err
			}
			// Fail fast on a misconfigured profile (permanent), but let the
			// scrape loop tolerate a configured-but-unreachable VM (transient).
			if err := requireDialable(p); err != nil {
				return err
			}

			host, _ := os.Hostname()
			cfg := otel.Config{
				Profile:       p.Name,
				VMEndpoint:    fmt.Sprintf("%s:%d", p.Host, p.Port),
				CollectorHost: host,
				Endpoint:      endpoint,
				Insecure:      insecure,
				Interval:      interval,
				Signals:       sig,
			}

			parent := cmd.Context()
			if parent == nil {
				parent = context.Background()
			}
			ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			provider, err := otel.New(ctx, cfg)
			if err != nil {
				return err
			}
			defer func() {
				shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = provider.Shutdown(shCtx)
			}()

			if insecure {
				cmd.PrintErrln("xpc otel: WARNING --insecure disables TLS to the collector (lab use only)")
			}
			cmd.PrintErrf("xpc otel: exporting %s for profile %q to %s every %s\n",
				signalSummary(sig), p.Name, endpoint, interval)

			loop := &otelLoop{
				g:         g,
				provider:  provider,
				signals:   sig,
				services:  services,
				eventLogs: eventLogs,
				eventMax:  eventMax,
				interval:  interval,
				seen:      map[string]map[string]bool{},
				warnf:     cmd.PrintErrf,
			}
			return loop.run(ctx)
		},
	}
	c.Flags().StringVar(&endpoint, "endpoint", "localhost:4317", "OTLP gRPC collector endpoint (host:port)")
	c.Flags().StringVar(&protocol, "protocol", "grpc", "OTLP transport: grpc (http is Phase 2)")
	c.Flags().DurationVar(&interval, "interval", 30*time.Second, "Scrape/export interval")
	c.Flags().BoolVar(&insecure, "insecure", false, "Disable TLS to the collector (lab only)")
	c.Flags().StringVar(&signals, "signals", "metrics,logs", "Signals to export: metrics,logs")
	c.Flags().StringSliceVar(&services, "services", nil, "Services to report up/down (e.g. Spooler,Themes)")
	c.Flags().StringSliceVar(&eventLogs, "event-logs", []string{"Application", "System"}, "Event logs to export as OTLP logs")
	c.Flags().IntVar(&eventMax, "event-max", 50, "Max Event Log records to fetch per log per scrape")
	return c
}

// ---- scrape loop ----------------------------------------------------------

type otelLoop struct {
	g         *Globals
	provider  *otel.Provider
	signals   otel.SignalSet
	services  []string
	eventLogs []string
	eventMax  int
	interval  time.Duration

	// per-log dedup window: logName -> set of record keys seen last scrape.
	seen   map[string]map[string]bool
	seeded bool // first log scrape seeds `seen` without emitting (no backfill)

	warnf func(format string, a ...any)
}

func (l *otelLoop) run(ctx context.Context) error {
	l.scrapeOnce(ctx)
	ticker := time.NewTicker(l.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			l.scrapeOnce(ctx)
		}
	}
}

func (l *otelLoop) scrapeOnce(ctx context.Context) {
	// Bound each scrape so a wedged VM can't stall the loop past one interval.
	sctx, cancel := context.WithTimeout(ctx, l.interval)
	defer cancel()

	if l.signals.Metrics {
		l.provider.SetSnapshot(l.scrapeMetrics(sctx))
	}
	if l.signals.Logs {
		l.scrapeLogs(sctx)
	}
}

func (l *otelLoop) scrapeMetrics(ctx context.Context) *otel.Snapshot {
	start := time.Now()
	snap := &otel.Snapshot{}

	cpu, mem, err := l.scrapeTypeperf(ctx)
	if err != nil {
		// Treat a failed host scrape as the VM being unreachable; still emit
		// the snapshot so xpc.vm.reachable=0 reaches the backend.
		l.warnf("xpc otel: scrape (typeperf) failed: %v\n", err)
		snap.ScrapeSeconds = time.Since(start).Seconds()
		return snap
	}
	snap.Reachable = true
	snap.CPUUtil = cpu
	snap.MemAvailable = mem

	if procs, err := l.scrapeProcesses(ctx); err != nil {
		l.warnf("xpc otel: scrape (tasklist) failed: %v\n", err)
	} else {
		snap.Processes = procs
		snap.ProcessCount = len(procs)
	}

	if len(l.services) > 0 {
		snap.Services = l.scrapeServices(ctx)
	}

	snap.ScrapeSeconds = time.Since(start).Seconds()
	return snap
}

func (l *otelLoop) scrapeTypeperf(ctx context.Context) (*float64, *int64, error) {
	// Run typeperf with an explicit argv via the python subprocess passthrough
	// (shell=False) instead of cmd.exe. cmd.exe mangles the counter path's "%"
	// and spaces -- "\Processor(_Total)\% Processor Time" reaches typeperf
	// truncated at "\%", so it reports "No valid counters" and exits 0xF0000002.
	// Same cmd.exe command-line quoting bug worked around for reg.exe in
	// runRegPassthrough; the counters themselves are healthy.
	py := buildSubprocessPy([]string{
		"typeperf",
		`\Processor(_Total)\% Processor Time`,
		`\Memory\Available Bytes`,
		"-sc", "1",
	})
	stdout, stderr, rc, err := runRemoteCmd(ctx, l.g, py, "python")
	if err != nil {
		return nil, nil, err
	}
	if rc != 0 {
		return nil, nil, fmt.Errorf("typeperf rc=%d: %s", rc, trimNewlines(stderr))
	}
	cpuPct, mem := parseTypeperf(stdout)
	var cpu *float64
	if cpuPct != nil {
		ratio := *cpuPct / 100.0
		cpu = &ratio
	}
	return cpu, mem, nil
}

func (l *otelLoop) scrapeProcesses(ctx context.Context) ([]otel.ProcSample, error) {
	stdout, stderr, rc, err := runRemoteCmd(ctx, l.g, "tasklist /v /fo csv /nh", "cmd")
	if err != nil {
		return nil, err
	}
	if rc != 0 {
		return nil, fmt.Errorf("tasklist rc=%d: %s", rc, trimNewlines(stderr))
	}
	procs, err := parseTasklistCSV(stdout)
	if err != nil {
		return nil, err
	}
	out := make([]otel.ProcSample, 0, len(procs))
	for _, p := range procs {
		out = append(out, otel.ProcSample{
			Name:     p.Name,
			PID:      p.PID,
			MemBytes: int64(p.MemoryKB) * 1024,
		})
	}
	return out, nil
}

func (l *otelLoop) scrapeServices(ctx context.Context) []otel.ServiceSample {
	parts := make([]string, len(l.services))
	for i, n := range l.services {
		parts[i] = "sc query " + n
	}
	stdout, _, _, err := runRemoteCmd(ctx, l.g, strings.Join(parts, " & "), "cmd")
	running := map[string]bool{}
	if err == nil {
		running = parseScQuery(stdout)
	} else {
		l.warnf("xpc otel: scrape (sc query) failed: %v\n", err)
	}
	out := make([]otel.ServiceSample, 0, len(l.services))
	for _, n := range l.services {
		out = append(out, otel.ServiceSample{Name: n, Up: running[strings.ToLower(n)]})
	}
	return out
}

func (l *otelLoop) scrapeLogs(ctx context.Context) {
	for _, logName := range l.eventLogs {
		entries, err := l.scrapeEventLog(ctx, logName)
		if err != nil {
			l.warnf("xpc otel: scrape (event log %s) failed: %v\n", logName, err)
			continue
		}
		prev := l.seen[logName]
		cur := make(map[string]bool, len(entries))
		var fresh []otel.LogEntry
		for _, e := range entries {
			k := logKey(e)
			cur[k] = true
			if prev == nil || !prev[k] {
				fresh = append(fresh, e)
			}
		}
		l.seen[logName] = cur
		if l.seeded && len(fresh) > 0 {
			l.provider.EmitLogs(ctx, fresh)
		}
	}
	// First pass only seeds the dedup window; it does not emit the backlog.
	l.seeded = true
}

func (l *otelLoop) scrapeEventLog(ctx context.Context, logName string) ([]otel.LogEntry, error) {
	parts := []string{`cscript /nologo C:\WINDOWS\system32\eventquery.vbs`, "/FO", "CSV"}
	if logName != "" {
		parts = append(parts, "/L", logName)
	}
	if l.eventMax > 0 {
		parts = append(parts, "/R", strconv.Itoa(l.eventMax))
	}
	stdout, stderr, rc, err := runRemoteCmd(ctx, l.g, strings.Join(parts, " "), "cmd")
	if err != nil {
		return nil, err
	}
	if rc != 0 {
		return nil, fmt.Errorf("eventquery rc=%d: %s", rc, trimNewlines(stderr))
	}
	return parseEventCSV(stdout, logName), nil
}

// ---- parsing helpers ------------------------------------------------------

// parseTypeperf reads `typeperf ... -sc 1` PDH-CSV output. The header row's
// first field contains "PDH-CSV"; the data row is "timestamp",cpu,mem.
func parseTypeperf(text string) (cpuPct *float64, memBytes *int64) {
	// Windows Python text-mode stdout turns typeperf's \r\n into \r\r\n; the
	// doubled CR breaks encoding/csv, which then parses no data row. Strip all
	// CRs so records are plain \n-terminated.
	text = strings.ReplaceAll(text, "\r", "")
	r := csv.NewReader(strings.NewReader(text))
	r.FieldsPerRecord = -1
	for {
		row, err := r.Read()
		if err != nil {
			break
		}
		if len(row) < 3 {
			continue
		}
		if strings.Contains(row[0], "PDH-CSV") {
			continue // header
		}
		cpu, cpuErr := strconv.ParseFloat(strings.TrimSpace(row[1]), 64)
		mem, memErr := strconv.ParseFloat(strings.TrimSpace(row[2]), 64)
		if cpuErr != nil || memErr != nil {
			continue
		}
		c := cpu
		m := int64(mem)
		return &c, &m
	}
	return nil, nil
}

// parseScQuery maps lowercased service name -> running, from `sc query`
// output (one or more SERVICE_NAME blocks).
func parseScQuery(out string) map[string]bool {
	res := map[string]bool{}
	blocks := strings.Split(out, "SERVICE_NAME:")
	for _, b := range blocks[1:] {
		first := b
		if nl := strings.IndexByte(b, '\n'); nl >= 0 {
			first = b[:nl]
		}
		name := strings.TrimSpace(first)
		if name == "" {
			continue
		}
		res[strings.ToLower(name)] = strings.Contains(b, "RUNNING")
	}
	return res
}

// parseEventCSV defensively parses eventquery.vbs /FO CSV output. Column order
// is locale/version dependent, so rather than assume positions it locates the
// type token, then classifies the remaining fields (event id = all digits,
// timestamp = first parseable date, the rest = source + body).
func parseEventCSV(text, logName string) []otel.LogEntry {
	r := csv.NewReader(strings.NewReader(text))
	r.FieldsPerRecord = -1
	var entries []otel.LogEntry
	for {
		row, err := r.Read()
		if err != nil {
			break
		}
		typeIdx := -1
		for i, f := range row {
			if isEventType(strings.TrimSpace(f)) {
				typeIdx = i
				break
			}
		}
		if typeIdx < 0 {
			continue // header row or noise
		}
		e := otel.LogEntry{LogName: logName, Type: strings.TrimSpace(row[typeIdx])}
		var body []string
		for i, f := range row {
			if i == typeIdx {
				continue
			}
			f = strings.TrimSpace(f)
			if f == "" {
				continue
			}
			if e.EventID == "" && isAllDigits(f) {
				e.EventID = f
				continue
			}
			if e.Timestamp.IsZero() {
				if t, ok := parseEventTime(f); ok {
					e.Timestamp = t
					continue
				}
			}
			body = append(body, f)
		}
		if len(body) > 0 {
			e.Source = body[0]
			e.Body = strings.Join(body, " | ")
		}
		entries = append(entries, e)
	}
	return entries
}

var eventTimeLayouts = []string{
	"1/2/2006 3:04:05 PM",
	"1/2/2006 15:04:05",
	"01/02/2006 15:04:05",
	"1/2/2006 3:04:05PM",
	"2006-01-02 15:04:05",
}

func parseEventTime(s string) (time.Time, bool) {
	for _, layout := range eventTimeLayouts {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func isEventType(s string) bool {
	switch s {
	case "Information", "Warning", "Error", "Success Audit", "Failure Audit":
		return true
	}
	return false
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// logKey is a stable dedup key for one Event Log record across scrapes.
func logKey(e otel.LogEntry) string {
	return strings.Join([]string{e.LogName, e.EventID, e.Timestamp.Format(time.RFC3339), e.Body}, "|")
}

// ---- signal flag parsing --------------------------------------------------

func parseSignals(s string) (otel.SignalSet, error) {
	var set otel.SignalSet
	for _, tok := range strings.Split(s, ",") {
		switch strings.TrimSpace(strings.ToLower(tok)) {
		case "metrics", "metric":
			set.Metrics = true
		case "logs", "log":
			set.Logs = true
		case "":
			// tolerate trailing commas
		default:
			return set, fmt.Errorf("--signals: unknown signal %q (want metrics and/or logs)", tok)
		}
	}
	if !set.Metrics && !set.Logs {
		return set, fmt.Errorf("--signals: select at least one of metrics,logs")
	}
	return set, nil
}

func signalSummary(s otel.SignalSet) string {
	switch {
	case s.Metrics && s.Logs:
		return "metrics+logs"
	case s.Metrics:
		return "metrics"
	default:
		return "logs"
	}
}
