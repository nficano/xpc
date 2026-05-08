package arcp

import (
	"strings"
	"testing"
	"time"
)

func TestNewIDFormat(t *testing.T) {
	t.Parallel()
	cases := []IDPrefix{PrefixMessage, PrefixSession, PrefixJob, PrefixStream, PrefixTrace, PrefixSpan}
	for _, p := range cases {
		p := p
		t.Run(string(p), func(t *testing.T) {
			t.Parallel()
			got, err := NewID(p)
			if err != nil {
				t.Fatalf("NewID: %v", err)
			}
			prefix, body := SplitID(got)
			if prefix != string(p) {
				t.Fatalf("prefix = %q; want %q (full id %q)", prefix, p, got)
			}
			if len(body) != 26 {
				t.Fatalf("body length = %d; want 26 (full id %q)", len(body), got)
			}
		})
	}
}

func TestNewIDUnique(t *testing.T) {
	t.Parallel()
	seen := make(map[string]struct{})
	for i := 0; i < 1000; i++ {
		id, err := NewID(PrefixMessage)
		if err != nil {
			t.Fatalf("NewID: %v", err)
		}
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate id %q at i=%d", id, i)
		}
		seen[id] = struct{}{}
	}
}

func TestNewIDRejectsEmptyPrefix(t *testing.T) {
	t.Parallel()
	if _, err := NewID(""); err == nil {
		t.Fatal("expected error on empty prefix")
	}
}

func TestSplitIDMalformed(t *testing.T) {
	t.Parallel()
	cases := []string{"", "noseparator", "_leadingunderscore", "trailingunderscore_"}
	for _, c := range cases {
		c := c
		t.Run(c, func(t *testing.T) {
			t.Parallel()
			pre, body := SplitID(c)
			if pre != "" || body != "" {
				t.Fatalf("SplitID(%q) = (%q, %q); want both empty", c, pre, body)
			}
		})
	}
}

func TestFormatTimestamp(t *testing.T) {
	t.Parallel()
	when := time.Date(2026, time.May, 8, 18, 21, 0, 123456*1000, time.UTC)
	got := FormatTimestamp(when)
	want := "2026-05-08T18:21:00.123456Z"
	if got != want {
		t.Fatalf("got %q; want %q", got, want)
	}
	// Timezone-stripping check: even if the input is in a non-UTC zone, the
	// output is UTC.
	pst := time.FixedZone("PST", -8*60*60)
	when2 := time.Date(2026, time.May, 8, 10, 21, 0, 0, pst)
	if !strings.HasSuffix(FormatTimestamp(when2), "Z") {
		t.Fatalf("FormatTimestamp didn't produce UTC: %s", FormatTimestamp(when2))
	}
}
