package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestParseMode(t *testing.T) {
	t.Parallel()
	cases := map[string]Mode{
		"":      ModeText,
		"text":  ModeText,
		"TEXT":  ModeText,
		"json":  ModeJSON,
		"JSON":  ModeJSON,
		"table": ModeTable,
		"weird": ModeText,
	}
	for in, want := range cases {
		if got := ParseMode(in); got != want {
			t.Errorf("ParseMode(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestEncode_JSON(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := Encode(&buf, ModeJSON, map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	var got map[string]int
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if got["a"] != 1 {
		t.Fatalf("got %v", got)
	}
}

func TestEncodeRows_JSON(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	headers := []string{"name", "pid"}
	rows := [][]any{
		{"a.exe", 100},
		{"b.exe", 200},
	}
	if err := EncodeRows(&buf, ModeJSON, headers, rows); err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if len(got) != 2 || got[0]["name"] != "a.exe" || got[1]["pid"].(float64) != 200 {
		t.Fatalf("unexpected: %v", got)
	}
}

func TestEncodeRows_Table(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	headers := []string{"name", "pid"}
	rows := [][]any{{"a.exe", 100}}
	if err := EncodeRows(&buf, ModeTable, headers, rows); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "name") || !strings.Contains(out, "a.exe") || !strings.Contains(out, "----") {
		t.Fatalf("missing header/separator/row: %q", out)
	}
}

func TestEncodeKV_AllModes(t *testing.T) {
	t.Parallel()
	pairs := []KV{{"name", "xpc"}, {"version", "0.1.0"}}

	var jsonBuf bytes.Buffer
	if err := EncodeKV(&jsonBuf, ModeJSON, pairs); err != nil {
		t.Fatal(err)
	}
	var jm map[string]string
	if err := json.Unmarshal(jsonBuf.Bytes(), &jm); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, jsonBuf.String())
	}
	if jm["name"] != "xpc" || jm["version"] != "0.1.0" {
		t.Fatalf("got %v", jm)
	}

	var textBuf bytes.Buffer
	if err := EncodeKV(&textBuf, ModeText, pairs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(textBuf.String(), "name: xpc") {
		t.Fatalf("text mode missing key: %q", textBuf.String())
	}
}
