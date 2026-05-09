package cli

import (
	"strings"
	"testing"
)

func TestDedupeEvtLines_FirstPollKeepsHeader(t *testing.T) {
	t.Parallel()
	in := "Listing the events in 'application' log\r\n" +
		"Type           Event   Date         Time         Source\r\n" +
		"---------------------------------------------------------\r\n" +
		"Information    100     5/9/2026     1:23:45 PM   xpc\r\n" +
		"Information    101     5/9/2026     1:23:46 PM   xpc\r\n"
	seen := map[string]struct{}{}
	printed, fresh := dedupeEvtLines(in, seen)
	if !strings.Contains(printed, "Listing the events") {
		t.Fatal("first poll should keep header")
	}
	if len(fresh) != 2 {
		t.Fatalf("expected 2 fresh records, got %d: %v", len(fresh), fresh)
	}
}

func TestDedupeEvtLines_SecondPollSkipsKnownRecords(t *testing.T) {
	t.Parallel()
	in := "Type   Event\r\n" +
		"Information    100     5/9/2026     xpc\r\n" +
		"Information    101     5/9/2026     xpc\r\n"
	seen := map[string]struct{}{
		"Information    100     5/9/2026     xpc": {},
	}
	printed, fresh := dedupeEvtLines(in, seen)
	if strings.Contains(printed, "Type   Event") {
		t.Fatal("second poll should suppress header")
	}
	if len(fresh) != 1 || !strings.Contains(fresh[0], "101") {
		t.Fatalf("unexpected fresh: %v", fresh)
	}
}

func TestLooksLikeEvtRecord(t *testing.T) {
	t.Parallel()
	yes := []string{"Information 100 ...", "Warning 200 x", "Error 300 y", "Success Audit 5 ok", "Failure Audit 5 bad"}
	no := []string{"", "Type Event Date", "----- ----- -----", "Listing the events"}
	for _, s := range yes {
		if !looksLikeEvtRecord(s) {
			t.Errorf("expected record: %q", s)
		}
	}
	for _, s := range no {
		if looksLikeEvtRecord(s) {
			t.Errorf("expected non-record: %q", s)
		}
	}
}
