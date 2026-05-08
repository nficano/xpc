package arcp

import (
	"strings"
	"testing"
)

func newSampleEnvelope() *Envelope {
	e := New("msg_01HABCDEF1234567890ABCDEF", TypePing, "2026-05-08T18:21:00.000000Z")
	e.Payload = map[string]interface{}{}
	return e
}

func TestNewSetsRequiredFields(t *testing.T) {
	t.Parallel()
	e := newSampleEnvelope()
	if e.ARCP != Version {
		t.Fatalf("ARCP = %q; want %q", e.ARCP, Version)
	}
	if e.Auth.Alg != AuthAlg || e.Auth.Kid != AuthKID {
		t.Fatalf("auth = %+v; want alg=%s kid=%s", e.Auth, AuthAlg, AuthKID)
	}
	if err := e.Validate(); err != nil {
		t.Fatalf("Validate after New: %v", err)
	}
}

func TestValidateRejectsMissingFields(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		mut  func(*Envelope)
		want string
	}{
		{"empty arcp", func(e *Envelope) { e.ARCP = "" }, "unsupported version"},
		{"wrong arcp version", func(e *Envelope) { e.ARCP = "9.9" }, "unsupported version"},
		{"empty id", func(e *Envelope) { e.ID = "" }, "empty envelope id"},
		{"empty type", func(e *Envelope) { e.Type = "" }, "empty envelope type"},
		{"empty timestamp", func(e *Envelope) { e.Timestamp = "" }, "empty timestamp"},
		{"unsupported alg", func(e *Envelope) { e.Auth.Alg = "MD5" }, "unsupported auth alg"},
		{"unsupported kid", func(e *Envelope) { e.Auth.Kid = "v99" }, "unsupported auth kid"},
		{"nil payload", func(e *Envelope) { e.Payload = nil }, "nil payload"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newSampleEnvelope()
			tc.mut(e)
			err := e.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v; want substring %q", err, tc.want)
			}
		})
	}
}

func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	t.Parallel()
	e := newSampleEnvelope()
	e.SessionID = "sess_abc"
	e.JobID = "job_xyz"
	e.Payload = map[string]interface{}{
		"key":  "value",
		"nest": map[string]interface{}{"a": 1.0, "b": []interface{}{"x", "y"}},
	}

	b, err := e.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got, err := Unmarshal(b)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.ID != e.ID || got.Type != e.Type || got.SessionID != e.SessionID || got.JobID != e.JobID {
		t.Fatalf("envelope mismatch:\n got %+v\nwant %+v", got, e)
	}
	if got.Payload["key"] != "value" {
		t.Fatalf("payload[key] = %v; want %q", got.Payload["key"], "value")
	}
}

func TestMarshalOmitsEmptyOptionalFields(t *testing.T) {
	t.Parallel()
	e := newSampleEnvelope()
	b, err := e.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(b)

	for _, key := range []string{"session_id", "job_id", "stream_id", "trace_id", "span_id", "parent_span_id", "correlation_id", "causation_id", "source", "target"} {
		if strings.Contains(got, "\""+key+"\"") {
			t.Fatalf("output unexpectedly contains optional field %q: %s", key, got)
		}
	}
}

func TestUnmarshalRejectsMalformedJSON(t *testing.T) {
	t.Parallel()
	if _, err := Unmarshal([]byte("not json")); err == nil {
		t.Fatal("expected error on malformed JSON")
	}
}

func TestIsKnownType(t *testing.T) {
	t.Parallel()
	for _, ty := range AllTypes {
		if !IsKnownType(ty) {
			t.Fatalf("IsKnownType(%q) = false; want true", ty)
		}
	}
	if IsKnownType("does.not.exist") {
		t.Fatal("IsKnownType('does.not.exist') = true; want false")
	}
}
