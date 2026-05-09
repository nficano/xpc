package arcp

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// canonicalMarshal encodes a value as JSON with sorted keys at every level
// and no HTML escaping (so '<', '>', '&' stay as-is). The result must be
// byte-identical between Go and Python to keep HMAC signatures consistent.
//
// Implementation: marshal -> unmarshal into any -> marshal again. The
// second marshal pass operates on map[string]any values, which the
// stdlib serializes with sorted keys.
func canonicalMarshal(v any) ([]byte, error) {
	first, err := jsonMarshalNoHTMLEscape(v)
	if err != nil {
		return nil, err
	}

	var generic any
	if err := json.Unmarshal(first, &generic); err != nil {
		return nil, fmt.Errorf("arcp: canonical roundtrip unmarshal: %w", err)
	}

	return jsonMarshalNoHTMLEscape(generic)
}

func jsonMarshalNoHTMLEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("arcp: marshal: %w", err)
	}
	out := buf.Bytes()
	// json.Encoder.Encode appends '\n'; strip it.
	if len(out) > 0 && out[len(out)-1] == '\n' {
		out = out[:len(out)-1]
	}
	return out, nil
}

// canonicalSigningInput returns the bytes that HMAC-SHA256 is computed over:
// the envelope JSON with auth.sig replaced by the empty string, sorted keys,
// no whitespace.
func canonicalSigningInput(e *Envelope) ([]byte, error) {
	if e == nil {
		return nil, fmt.Errorf("arcp: nil envelope")
	}
	clone := *e
	clone.Auth.Sig = ""
	return canonicalMarshal(&clone)
}
