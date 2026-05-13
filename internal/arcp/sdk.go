package arcp

import (
	"encoding/json"
	"fmt"
	"time"

	sdk "github.com/agentruntimecontrolprotocol/go-sdk"
)

const sdkXPCExtension = "arcpx.nficano.xpc.v0"

type sdkRawPayload struct {
	msgType string
	raw     json.RawMessage
}

func (p sdkRawPayload) ARCPType() string { return p.msgType }

func (p sdkRawPayload) MarshalJSON() ([]byte, error) {
	if len(p.raw) == 0 {
		return []byte("{}"), nil
	}
	return p.raw, nil
}

// ToSDKEnvelope converts an xpc v0 envelope into the official ARCP SDK
// envelope type. It is intended for host-side adapters and observers; it does
// not change the VM wire protocol.
func ToSDKEnvelope(e *Envelope) (sdk.Envelope, error) {
	if err := e.Validate(); err != nil {
		return sdk.Envelope{}, err
	}
	ts, err := time.Parse(time.RFC3339Nano, e.Timestamp)
	if err != nil {
		return sdk.Envelope{}, fmt.Errorf("arcp: parse timestamp for SDK envelope: %w", err)
	}
	payload, err := json.Marshal(e.Payload)
	if err != nil {
		return sdk.Envelope{}, fmt.Errorf("arcp: marshal payload for SDK envelope: %w", err)
	}
	extensions, err := sdkExtensions(e)
	if err != nil {
		return sdk.Envelope{}, err
	}
	return sdk.Envelope{
		ID:            sdk.MessageID(e.ID),
		Timestamp:     ts,
		Source:        e.Source,
		Target:        e.Target,
		SessionID:     sdk.SessionID(e.SessionID),
		JobID:         sdk.JobID(e.JobID),
		StreamID:      sdk.StreamID(e.StreamID),
		TraceID:       sdk.TraceID(e.TraceID),
		SpanID:        sdk.SpanID(e.SpanID),
		ParentSpanID:  sdk.SpanID(e.ParentSpanID),
		CorrelationID: sdk.MessageID(e.CorrelationID),
		CausationID:   sdk.MessageID(e.CausationID),
		Extensions:    extensions,
		Payload: sdkRawPayload{
			msgType: e.Type,
			raw:     payload,
		},
	}, nil
}

// FromSDKEnvelope converts an official ARCP SDK envelope into an xpc v0
// envelope. The returned envelope is unsigned; callers must Sign it before
// sending it across the current xpc VM transport.
func FromSDKEnvelope(e sdk.Envelope) (*Envelope, error) {
	if e.ID == "" {
		return nil, fmt.Errorf("arcp: SDK envelope has empty id")
	}
	if e.Payload == nil {
		return nil, fmt.Errorf("arcp: SDK envelope has nil payload")
	}
	if e.Timestamp.IsZero() {
		return nil, fmt.Errorf("arcp: SDK envelope has zero timestamp")
	}
	msgType := e.Type()
	if msgType == "" {
		return nil, fmt.Errorf("arcp: SDK envelope has empty type")
	}
	payloadBytes, err := json.Marshal(e.Payload)
	if err != nil {
		return nil, fmt.Errorf("arcp: marshal SDK payload: %w", err)
	}
	payload := map[string]any{}
	if len(payloadBytes) > 0 {
		if err := json.Unmarshal(payloadBytes, &payload); err != nil {
			return nil, fmt.Errorf("arcp: decode SDK payload: %w", err)
		}
	}
	out := New(string(e.ID), msgType, formatSDKTimestamp(e.Timestamp))
	out.SessionID = string(e.SessionID)
	out.JobID = string(e.JobID)
	out.StreamID = string(e.StreamID)
	out.TraceID = string(e.TraceID)
	out.SpanID = string(e.SpanID)
	out.ParentSpanID = string(e.ParentSpanID)
	out.CorrelationID = string(e.CorrelationID)
	out.CausationID = string(e.CausationID)
	out.Source = e.Source
	out.Target = e.Target
	out.Payload = payload
	return out, nil
}

func sdkExtensions(e *Envelope) (map[string]json.RawMessage, error) {
	if e.Auth.Alg == "" && e.Auth.Kid == "" {
		return nil, nil
	}
	meta := struct {
		AuthAlg string `json:"auth_alg,omitempty"`
		AuthKID string `json:"auth_kid,omitempty"`
	}{
		AuthAlg: e.Auth.Alg,
		AuthKID: e.Auth.Kid,
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("arcp: marshal xpc SDK extension: %w", err)
	}
	return map[string]json.RawMessage{
		sdkXPCExtension: raw,
	}, nil
}

func formatSDKTimestamp(ts time.Time) string {
	return ts.UTC().Format("2006-01-02T15:04:05.000000Z")
}
