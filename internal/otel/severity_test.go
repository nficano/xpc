package otel

import (
	"testing"

	otellog "go.opentelemetry.io/otel/log"
)

func TestSeverityFor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		want    otellog.Severity
		wantTxt string
	}{
		{"Error", otellog.SeverityError, "Error"},
		{"Failure Audit", otellog.SeverityError, "Failure Audit"},
		{"Warning", otellog.SeverityWarn, "Warning"},
		{"Information", otellog.SeverityInfo, "Information"},
		{"Success Audit", otellog.SeverityInfo, "Success Audit"},
		{"  Error  ", otellog.SeverityError, "Error"},
		{"", otellog.SeverityUndefined, ""},
		{"Unknown", otellog.SeverityInfo, "Unknown"},
	}
	for _, tt := range tests {
		sev, txt := severityFor(tt.in)
		if sev != tt.want || txt != tt.wantTxt {
			t.Errorf("severityFor(%q) = (%v,%q), want (%v,%q)", tt.in, sev, txt, tt.want, tt.wantTxt)
		}
	}
}

func TestBoolToInt(t *testing.T) {
	t.Parallel()
	if boolToInt(true) != 1 || boolToInt(false) != 0 {
		t.Fatal("boolToInt mapping wrong")
	}
}
