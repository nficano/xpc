package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nficano/xpc/internal/arcp"
)

func TestParseToolDescriptors(t *testing.T) {
	payload := map[string]interface{}{
		"tools": []interface{}{
			map[string]interface{}{
				"name":        "exec",
				"description": "Run a command.",
				"input_schema": map[string]interface{}{
					"type":     "object",
					"required": []interface{}{"cmd"},
				},
			},
			map[string]interface{}{
				"name": "agent.info",
				// no description, no schema
			},
			map[string]interface{}{
				// missing name -> dropped
				"description": "ghost",
			},
			"not-an-object", // also dropped
		},
	}
	got := parseToolDescriptors(payload)
	if len(got) != 2 {
		t.Fatalf("want 2 descriptors, got %d", len(got))
	}
	if got[0].Name != "exec" || got[0].Description != "Run a command." {
		t.Errorf("descriptor[0] = %+v", got[0])
	}
	if got[0].InputSchema["type"] != "object" {
		t.Errorf("descriptor[0] schema lost type: %+v", got[0].InputSchema)
	}
	if got[1].Name != "agent.info" {
		t.Errorf("descriptor[1] name = %q", got[1].Name)
	}
	if got[1].InputSchema["type"] != "object" {
		t.Errorf("descriptor[1] should default to object schema")
	}
}

func TestBuildToolCallResult_CompletedWithStreams(t *testing.T) {
	call := &toolCall{
		streamChannels: map[string]string{},
		terminal:       "completed",
		resultPayload:  map[string]interface{}{"exit_code": float64(0), "timed_out": false},
	}
	call.stdout.WriteString("Volume in drive C is...\r\n")
	call.stderr.WriteString("warning\n")

	result := buildToolCallResult(call)
	if result["isError"] == true {
		t.Errorf("completed call must not be flagged isError")
	}
	text := firstContentText(t, result)
	if !strings.Contains(text, `"exit_code":0`) {
		t.Errorf("expected exit_code in output: %q", text)
	}
	if !strings.Contains(text, "Volume in drive C") {
		t.Errorf("expected stdout in output: %q", text)
	}
	if !strings.Contains(text, "warning") {
		t.Errorf("expected stderr in output: %q", text)
	}
}

func TestBuildToolCallResult_ToolError(t *testing.T) {
	call := &toolCall{
		streamChannels: map[string]string{},
		terminal:       "failed",
		toolErr:        &toolErrInfo{Code: "INVALID_ARGS", Message: "missing 'cmd'"},
	}
	result := buildToolCallResult(call)
	if result["isError"] != true {
		t.Errorf("tool.error must surface as isError=true")
	}
	text := firstContentText(t, result)
	if !strings.Contains(text, "INVALID_ARGS") || !strings.Contains(text, "missing 'cmd'") {
		t.Errorf("missing code/message in output: %q", text)
	}
}

func TestBuildToolCallResult_Cancelled(t *testing.T) {
	call := &toolCall{streamChannels: map[string]string{}, terminal: "cancelled"}
	result := buildToolCallResult(call)
	if result["isError"] != true {
		t.Errorf("cancelled call must surface as isError=true")
	}
}

func TestBuildToolCallResult_NoOutput(t *testing.T) {
	call := &toolCall{streamChannels: map[string]string{}, terminal: "completed"}
	result := buildToolCallResult(call)
	text := firstContentText(t, result)
	if text != "(no output)" {
		t.Errorf("empty completion should yield placeholder, got %q", text)
	}
}

// TestRouteEnvelope_FullJobLifecycle drives a fake call through the routing
// layer and asserts that stream chunks accumulate, the job_id index is
// populated on job.accepted, and job.completed signals done.
func TestRouteEnvelope_FullJobLifecycle(t *testing.T) {
	const invokeID = "msg_01invoke11111111111111aa"
	const jobID = "job_01job1111111111111111aa"
	const stdoutStream = "str_01stream1111111111111aa"

	call := &toolCall{
		streamChannels: map[string]string{},
		done:           make(chan struct{}),
	}
	b := &mcpBridge{
		calls:   map[string]*toolCall{invokeID: call},
		jobs:    map[string]*toolCall{},
		mcpReqs: map[string]*toolCall{},
	}

	// job.accepted populates the jobID index.
	b.routeEnvelope(&arcp.Envelope{
		Type: arcp.TypeJobAccepted, JobID: jobID, CorrelationID: invokeID,
		Payload: map[string]interface{}{"state": "accepted"},
	})
	if b.jobs[jobID] != call {
		t.Fatalf("job.accepted did not register jobID")
	}

	// stream.open registers a channel.
	b.routeEnvelope(&arcp.Envelope{
		Type: arcp.TypeStreamOpen, JobID: jobID, StreamID: stdoutStream,
		Payload: map[string]interface{}{"channel": "stdout"},
	})
	if call.streamChannels[stdoutStream] != "stdout" {
		t.Errorf("stream.open did not bind stream id to channel")
	}

	// stream.chunk routes by stream_id and accumulates.
	b.routeEnvelope(&arcp.Envelope{
		Type: arcp.TypeStreamChunk, JobID: jobID, StreamID: stdoutStream,
		Payload: map[string]interface{}{"delta": "hello "},
	})
	b.routeEnvelope(&arcp.Envelope{
		Type: arcp.TypeStreamChunk, JobID: jobID, StreamID: stdoutStream,
		Payload: map[string]interface{}{"delta": "world"},
	})
	if call.stdout.String() != "hello world" {
		t.Errorf("stream chunks not accumulated: %q", call.stdout.String())
	}

	// tool.result captures the payload.
	b.routeEnvelope(&arcp.Envelope{
		Type: arcp.TypeToolResult, JobID: jobID, CorrelationID: invokeID,
		Payload: map[string]interface{}{"exit_code": float64(0)},
	})

	// job.completed closes done.
	b.routeEnvelope(&arcp.Envelope{
		Type: arcp.TypeJobCompleted, JobID: jobID, CorrelationID: invokeID,
		Payload: map[string]interface{}{},
	})
	select {
	case <-call.done:
	default:
		t.Fatal("job.completed did not close done channel")
	}
	if call.terminal != "completed" {
		t.Errorf("terminal = %q, want completed", call.terminal)
	}
}

func firstContentText(t *testing.T, result map[string]interface{}) string {
	t.Helper()
	content, ok := result["content"].([]interface{})
	if !ok || len(content) == 0 {
		t.Fatalf("result has no content array: %+v", result)
	}
	first, ok := content[0].(map[string]interface{})
	if !ok {
		t.Fatalf("first content item not an object: %+v", content[0])
	}
	text, _ := first["text"].(string)
	return text
}

// TestJSONRPCRequestParsing ensures we tolerate the wire shapes that real MCP
// clients send (numeric ids, string ids, missing params).
func TestJSONRPCRequestParsing(t *testing.T) {
	cases := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":"abc","method":"tools/list"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`,
	}
	for _, raw := range cases {
		var req jsonrpcReq
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			t.Errorf("failed to parse %q: %v", raw, err)
		}
		if req.JSONRPC != "2.0" {
			t.Errorf("expected jsonrpc=2.0, got %q", req.JSONRPC)
		}
	}
}
