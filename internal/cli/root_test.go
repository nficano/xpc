package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/nficano/xpc/internal/arcp"
)

func TestARCPObserverIncludesSDKEnvelope(t *testing.T) {
	var buf bytes.Buffer
	obs := newARCPObserver(&buf)

	env := arcp.New("msg_debug", arcp.TypePing, "2026-05-13T16:00:01.123456Z")
	env.Payload = map[string]any{"note": "hello"}
	obs(arcp.DirSend, env)

	var record map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("decode observer record: %v", err)
	}
	if _, ok := record["envelope"]; !ok {
		t.Fatal("observer record missing legacy envelope")
	}
	var sdkEnvelope map[string]any
	if err := json.Unmarshal(record["sdk_envelope"], &sdkEnvelope); err != nil {
		t.Fatalf("decode SDK envelope: %v", err)
	}
	if sdkEnvelope["type"] != arcp.TypePing {
		t.Fatalf("SDK envelope type = %v; want %q", sdkEnvelope["type"], arcp.TypePing)
	}
	if _, ok := sdkEnvelope["auth"]; ok {
		t.Fatalf("SDK envelope unexpectedly contains legacy auth: %v", sdkEnvelope)
	}
}
