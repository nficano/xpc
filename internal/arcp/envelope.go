// Package arcp implements the xpc wire protocol per docs/PROTOCOL.md.
//
// The envelope shape is a v0 subset of ARCP RFC 0001. This package handles
// envelope encode/decode, JSON canonicalization, HMAC-SHA256 signing and
// verification, length-prefixed framing on TCP streams, and ID generation.
package arcp

import (
	"encoding/json"
	"fmt"
)

// Version is the ARCP protocol version this package implements.
const Version = "1.0"

// MaxEnvelopeBytes is the maximum size of a single envelope on the wire.
// Receivers must close the connection on overlength frames.
const MaxEnvelopeBytes = 50 * 1024 * 1024

// AuthAlg is the HMAC algorithm identifier for v0.
const AuthAlg = "HMAC-SHA256"

// AuthKID identifies the PSK in use; only "v0" is valid in v0.
const AuthKID = "v0"

// Auth carries the HMAC signature material on every envelope.
type Auth struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Sig string `json:"sig"`
}

// Envelope is the canonical ARCP message container.
//
// Required fields: ARCP, ID, Type, Timestamp, Auth, Payload.
// Conditional fields are emitted only when non-empty (see omitempty tags).
type Envelope struct {
	ARCP          string         `json:"arcp"`
	ID            string         `json:"id"`
	Type          string         `json:"type"`
	SessionID     string         `json:"session_id,omitempty"`
	JobID         string         `json:"job_id,omitempty"`
	StreamID      string         `json:"stream_id,omitempty"`
	TraceID       string         `json:"trace_id,omitempty"`
	SpanID        string         `json:"span_id,omitempty"`
	ParentSpanID  string         `json:"parent_span_id,omitempty"`
	CorrelationID string         `json:"correlation_id,omitempty"`
	CausationID   string         `json:"causation_id,omitempty"`
	Source        string         `json:"source,omitempty"`
	Target        string         `json:"target,omitempty"`
	Timestamp     string         `json:"timestamp"`
	Auth          Auth           `json:"auth"`
	Payload       map[string]any `json:"payload"`
}

// New builds an envelope with required fields populated. The caller fills in
// the optional fields and the payload, then calls Sign before sending.
//
// Timestamp must be supplied by the caller (RFC 3339 with microsecond
// precision and trailing 'Z'), so tests can inject deterministic times.
func New(id, msgType, timestamp string) *Envelope {
	return &Envelope{
		ARCP:      Version,
		ID:        id,
		Type:      msgType,
		Timestamp: timestamp,
		Auth: Auth{
			Alg: AuthAlg,
			Kid: AuthKID,
		},
		Payload: map[string]any{},
	}
}

// Validate checks that required envelope fields are populated and that no
// fields exceed schema constraints. It does NOT verify HMAC; use VerifySig.
func (e *Envelope) Validate() error {
	if e == nil {
		return fmt.Errorf("arcp: nil envelope")
	}
	if e.ARCP != Version {
		return fmt.Errorf("arcp: unsupported version %q (expected %q)", e.ARCP, Version)
	}
	if e.ID == "" {
		return fmt.Errorf("arcp: empty envelope id")
	}
	if e.Type == "" {
		return fmt.Errorf("arcp: empty envelope type")
	}
	if e.Timestamp == "" {
		return fmt.Errorf("arcp: empty timestamp")
	}
	if e.Auth.Alg != AuthAlg {
		return fmt.Errorf("arcp: unsupported auth alg %q", e.Auth.Alg)
	}
	if e.Auth.Kid != AuthKID {
		return fmt.Errorf("arcp: unsupported auth kid %q", e.Auth.Kid)
	}
	if e.Payload == nil {
		return fmt.Errorf("arcp: nil payload (use {} for empty)")
	}
	return nil
}

// Marshal encodes the envelope as JSON bytes using a sorted-keys
// canonicalization (no whitespace, no HTML escaping). The returned bytes are
// suitable for direct framing on the wire.
func (e *Envelope) Marshal() ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return canonicalMarshal(e)
}

// Unmarshal parses an envelope from JSON bytes. It does not verify auth.
func Unmarshal(b []byte) (*Envelope, error) {
	var e Envelope
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, fmt.Errorf("arcp: unmarshal: %w", err)
	}
	if e.Payload == nil {
		e.Payload = map[string]any{}
	}
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return &e, nil
}
