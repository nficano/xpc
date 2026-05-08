package arcp

import (
	"strings"
	"testing"
)

// TestCanonicalSortedKeys checks that canonicalization produces sorted-key
// JSON at every nesting level. This is the property that lets Go and Python
// produce byte-identical inputs to HMAC.
func TestCanonicalSortedKeys(t *testing.T) {
	t.Parallel()
	v := map[string]interface{}{
		"z": 1,
		"a": map[string]interface{}{"y": 2, "b": 3},
		"m": []interface{}{
			map[string]interface{}{"q": 9, "p": 8},
		},
	}
	got, err := canonicalMarshal(v)
	if err != nil {
		t.Fatalf("canonicalMarshal: %v", err)
	}
	want := `{"a":{"b":3,"y":2},"m":[{"p":8,"q":9}],"z":1}`
	if string(got) != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// TestCanonicalNoHTMLEscape checks that '<', '>', '&' are emitted literally,
// matching Python's json.dumps default (Python does not HTML-escape).
func TestCanonicalNoHTMLEscape(t *testing.T) {
	t.Parallel()
	v := map[string]interface{}{"k": "<hello & 'world'>"}
	got, err := canonicalMarshal(v)
	if err != nil {
		t.Fatalf("canonicalMarshal: %v", err)
	}
	// Reject Go's default HTML-escaped form: those are 6-character escape
	// sequences (backslash, u, four hex digits) embedded in the JSON bytes.
	for _, escape := range []string{"\\u003c", "\\u003e", "\\u0026"} {
		if strings.Contains(string(got), escape) {
			t.Fatalf("html-escape sequence %q present in output: %s", escape, got)
		}
	}
	want := `{"k":"<hello & 'world'>"}`
	if string(got) != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// TestCanonicalNoTrailingNewline ensures the encoder's trailing '\n' is stripped.
func TestCanonicalNoTrailingNewline(t *testing.T) {
	t.Parallel()
	got, err := canonicalMarshal(map[string]interface{}{"k": "v"})
	if err != nil {
		t.Fatalf("canonicalMarshal: %v", err)
	}
	if len(got) == 0 || got[len(got)-1] == '\n' {
		t.Fatalf("trailing newline present: %q", got)
	}
}

// TestCanonicalSigningInputBlanksSig verifies that auth.sig is set to "" in
// the bytes that go into HMAC.
func TestCanonicalSigningInputBlanksSig(t *testing.T) {
	t.Parallel()
	e := newSampleEnvelope()
	e.Auth.Sig = "deadbeef"
	got, err := canonicalSigningInput(e)
	if err != nil {
		t.Fatalf("canonicalSigningInput: %v", err)
	}
	if !strings.Contains(string(got), `"sig":""`) {
		t.Fatalf("expected sig:\"\" in canonical input; got %s", got)
	}
	if strings.Contains(string(got), "deadbeef") {
		t.Fatalf("real sig leaked into canonical input: %s", got)
	}
	if e.Auth.Sig != "deadbeef" {
		t.Fatal("canonicalSigningInput mutated the envelope")
	}
}
