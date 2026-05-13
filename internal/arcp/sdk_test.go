package arcp

import (
	"encoding/json"
	"testing"
	"time"

	sdk "github.com/agentruntimecontrolprotocol/go-sdk"
	"github.com/agentruntimecontrolprotocol/go-sdk/messages"
)

func TestToSDKEnvelopeUsesOfficialMarshalShape(t *testing.T) {
	env := New("msg_test", TypeToolInvoke, "2026-05-13T16:00:01.123456Z")
	env.SessionID = "sess_test"
	env.Auth.Sig = "deadbeef"
	env.Payload = map[string]any{
		"tool": "exec",
		"arguments": map[string]any{
			"cmd": "ver",
		},
	}

	got, err := ToSDKEnvelope(env)
	if err != nil {
		t.Fatalf("ToSDKEnvelope returned error: %v", err)
	}
	if got.Type() != TypeToolInvoke {
		t.Fatalf("SDK type = %q; want %q", got.Type(), TypeToolInvoke)
	}
	if got.SessionID != sdk.SessionID("sess_test") {
		t.Fatalf("SDK session id = %q; want sess_test", got.SessionID)
	}

	wireBytes, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal SDK envelope: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(wireBytes, &wire); err != nil {
		t.Fatalf("decode SDK wire JSON: %v", err)
	}
	if _, ok := wire["auth"]; ok {
		t.Fatalf("official SDK wire JSON unexpectedly included xpc auth: %s", wireBytes)
	}
	if wire["type"] != TypeToolInvoke {
		t.Fatalf("wire type = %v; want %q", wire["type"], TypeToolInvoke)
	}
	payload, ok := wire["payload"].(map[string]any)
	if !ok {
		t.Fatalf("payload = %T; want object", wire["payload"])
	}
	if payload["tool"] != "exec" {
		t.Fatalf("payload.tool = %v; want exec", payload["tool"])
	}
	extensions, ok := wire["extensions"].(map[string]any)
	if !ok {
		t.Fatalf("extensions = %T; want object", wire["extensions"])
	}
	xpcExt, ok := extensions[sdkXPCExtension].(map[string]any)
	if !ok {
		t.Fatalf("missing xpc extension %q in %v", sdkXPCExtension, extensions)
	}
	if xpcExt["auth_alg"] != AuthAlg || xpcExt["auth_kid"] != AuthKID {
		t.Fatalf("xpc auth metadata = %v; want alg/kid only", xpcExt)
	}
	if _, ok := xpcExt["sig"]; ok {
		t.Fatalf("xpc extension leaked auth signature: %v", xpcExt)
	}
}

func TestFromSDKEnvelopeBuildsUnsignedXPCEnvelope(t *testing.T) {
	ts := time.Date(2026, 5, 13, 16, 0, 1, 123456000, time.UTC)
	env := sdk.Envelope{
		ID:            sdk.MessageID("msg_sdk"),
		Timestamp:     ts,
		Source:        "codex",
		Target:        "xpc-agent",
		SessionID:     sdk.SessionID("sess_sdk"),
		CorrelationID: sdk.MessageID("msg_parent"),
		Payload: messages.ToolInvoke{
			Tool: "exec",
			Arguments: map[string]any{
				"cmd": "ver",
			},
		},
	}

	got, err := FromSDKEnvelope(env)
	if err != nil {
		t.Fatalf("FromSDKEnvelope returned error: %v", err)
	}
	if got.ID != "msg_sdk" || got.Type != TypeToolInvoke {
		t.Fatalf("id/type = %q/%q; want msg_sdk/%q", got.ID, got.Type, TypeToolInvoke)
	}
	if got.Timestamp != "2026-05-13T16:00:01.123456Z" {
		t.Fatalf("timestamp = %q", got.Timestamp)
	}
	if got.Auth.Alg != AuthAlg || got.Auth.Kid != AuthKID || got.Auth.Sig != "" {
		t.Fatalf("auth = %+v; want unsigned xpc defaults", got.Auth)
	}
	if got.Source != "codex" || got.Target != "xpc-agent" || got.SessionID != "sess_sdk" {
		t.Fatalf("routing fields not copied: %+v", got)
	}
	if got.CorrelationID != "msg_parent" {
		t.Fatalf("correlation id = %q; want msg_parent", got.CorrelationID)
	}
	args, ok := got.Payload["arguments"].(map[string]any)
	if !ok {
		t.Fatalf("arguments = %T; want object", got.Payload["arguments"])
	}
	if args["cmd"] != "ver" {
		t.Fatalf("arguments.cmd = %v; want ver", args["cmd"])
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("converted envelope should validate before signing: %v", err)
	}
}
