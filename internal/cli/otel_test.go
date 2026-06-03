package cli

import "testing"

func TestParseTypeperf(t *testing.T) {
	t.Parallel()
	in := `"(PDH-CSV 4.0)","\\XPVM\Processor(_Total)\% Processor Time","\\XPVM\Memory\Available Bytes"` + "\r\n" +
		`"05/26/2025 12:00:00.000","3.140000","268435456.000000"` + "\r\n" +
		"Exiting, please wait...\r\n" +
		"The command completed successfully.\r\n"
	cpu, mem := parseTypeperf(in)
	if cpu == nil || mem == nil {
		t.Fatalf("expected cpu+mem, got cpu=%v mem=%v", cpu, mem)
	}
	if *cpu < 3.13 || *cpu > 3.15 {
		t.Errorf("cpu = %v, want ~3.14", *cpu)
	}
	if *mem != 268435456 {
		t.Errorf("mem = %d, want 268435456", *mem)
	}
}

func TestParseTypeperf_NoData(t *testing.T) {
	t.Parallel()
	cpu, mem := parseTypeperf("Error: counter not found\r\n")
	if cpu != nil || mem != nil {
		t.Fatalf("expected nils, got cpu=%v mem=%v", cpu, mem)
	}
}

func TestParseScQuery(t *testing.T) {
	t.Parallel()
	in := "SERVICE_NAME: Spooler\r\n" +
		"        TYPE               : 110  WIN32_OWN_PROCESS (interactive)\r\n" +
		"        STATE              : 4  RUNNING\r\n" +
		"                                (STOPPABLE, PAUSABLE, ACCEPTS_SHUTDOWN)\r\n" +
		"SERVICE_NAME: Themes\r\n" +
		"        TYPE               : 20  WIN32_SHARE_PROCESS\r\n" +
		"        STATE              : 1  STOPPED\r\n"
	got := parseScQuery(in)
	if !got["spooler"] {
		t.Errorf("spooler should be running")
	}
	if got["themes"] {
		t.Errorf("themes should be stopped")
	}
}

func TestParseEventCSV(t *testing.T) {
	t.Parallel()
	in := `"Type","Event","Date Time","Source","ComputerName","Category","User","Description"` + "\r\n" +
		`"Error","7034","5/26/2025 1:23:45 PM","Service Control Manager","XPVM","None","N/A","The Print Spooler service terminated unexpectedly."` + "\r\n" +
		`"Information","100","5/26/2025 1:24:00 PM","xpc","XPVM","None","N/A","Agent started."` + "\r\n"
	entries := parseEventCSV(in, "System")
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (header skipped), got %d", len(entries))
	}
	e := entries[0]
	if e.Type != "Error" {
		t.Errorf("type = %q, want Error", e.Type)
	}
	if e.EventID != "7034" {
		t.Errorf("event id = %q, want 7034", e.EventID)
	}
	if e.Source != "Service Control Manager" {
		t.Errorf("source = %q, want Service Control Manager", e.Source)
	}
	if e.Timestamp.IsZero() {
		t.Errorf("timestamp should have parsed from %q", "5/26/2025 1:23:45 PM")
	}
	if e.LogName != "System" {
		t.Errorf("log name = %q, want System", e.LogName)
	}
	if entries[1].Type != "Information" {
		t.Errorf("second entry type = %q, want Information", entries[1].Type)
	}
}

func TestParseEventCSV_DedupKeyStable(t *testing.T) {
	t.Parallel()
	row := `"Warning","1","5/26/2025 1:00:00 PM","src","XPVM","None","N/A","body"` + "\r\n"
	a := parseEventCSV(row, "Application")
	b := parseEventCSV(row, "Application")
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("expected 1 entry each, got %d and %d", len(a), len(b))
	}
	if logKey(a[0]) != logKey(b[0]) {
		t.Errorf("logKey not stable: %q vs %q", logKey(a[0]), logKey(b[0]))
	}
}

func TestParseSignals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in            string
		metrics, logs bool
		wantErr       bool
	}{
		{"metrics,logs", true, true, false},
		{"metrics", true, false, false},
		{"logs", false, true, false},
		{"metrics,", true, false, false}, // trailing comma tolerated
		{"bogus", false, false, true},
		{"", false, false, true}, // no signal selected
	}
	for _, tt := range tests {
		got, err := parseSignals(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseSignals(%q): expected error", tt.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseSignals(%q): unexpected error %v", tt.in, err)
			continue
		}
		if got.Metrics != tt.metrics || got.Logs != tt.logs {
			t.Errorf("parseSignals(%q) = %+v, want metrics=%v logs=%v", tt.in, got, tt.metrics, tt.logs)
		}
	}
}
