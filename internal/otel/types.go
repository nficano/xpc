// Package otel turns scraped Windows XP telemetry into OpenTelemetry signals
// and exports them over OTLP. It is transport-agnostic: callers (internal/cli)
// scrape a VM over ARCP, build a Snapshot and []LogEntry from the command
// output, and hand them to a Provider. The Provider owns all OTel SDK state.
//
// See docs/protocol/RFC-0002-otel.md for the design this implements.
package otel

import "time"

// SignalSet selects which OTel signals a Provider exports.
type SignalSet struct {
	Metrics bool
	Logs    bool
}

// Config configures a Provider. Endpoint is the OTLP collector address
// ("host:port", no scheme); VMEndpoint and Profile describe the VM the
// signals are attributed to.
type Config struct {
	Profile       string            // VM profile name -> host.name / xpc.profile
	VMEndpoint    string            // VM host:port -> xpc.vm.endpoint resource attr
	CollectorHost string            // host running xpc -> xpc.collector.host
	Endpoint      string            // OTLP collector endpoint (host:port)
	Insecure      bool              // disable TLS to the collector (lab only)
	Interval      time.Duration     // metric export interval (PeriodicReader)
	Signals       SignalSet         // which signals to export
	Headers       map[string]string // optional OTLP headers (e.g. auth)
}

// Snapshot is one scrape cycle's worth of metric samples for a single VM.
// Pointer fields are nil when the underlying counter could not be read.
type Snapshot struct {
	CPUUtil       *float64 // 0..1 ratio (typeperf % Processor Time / 100)
	MemAvailable  *int64   // bytes (typeperf Memory\Available Bytes)
	ProcessCount  int
	Processes     []ProcSample
	Services      []ServiceSample
	Reachable     bool
	ScrapeSeconds float64 // wall-clock duration of the scrape
}

// ProcSample is one process row from `tasklist`.
type ProcSample struct {
	Name     string
	PID      int
	MemBytes int64
}

// ServiceSample is one service's up/down state from `sc query`.
type ServiceSample struct {
	Name string
	Up   bool
}

// LogEntry is one Windows Event Log record, ready to map to an OTLP LogRecord.
// Type is the raw Windows type token ("Error", "Warning", "Information",
// "Success Audit", "Failure Audit"); the Provider maps it to a severity.
type LogEntry struct {
	Timestamp time.Time
	Type      string
	EventID   string
	Source    string
	Category  string
	Body      string
	LogName   string // Application | System | Security | ...
}
