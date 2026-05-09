// Command gen-corpus produces the shared protocol test corpus consumed by
// both internal/arcp/corpus_test.go and agent/tests/test_corpus.py.
//
// Run:
//
//	go run ./cmd/gen-corpus > tests/protocol_corpus.json
//
// Each test case includes an input envelope, the expected canonical signing
// bytes, the expected HMAC, and the expected framed wire bytes. Both
// language implementations of the protocol must produce byte-identical
// output for every case.
package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"github.com/nficano/xpc/internal/arcp"
)

const fixedTimestamp = "2026-05-08T18:21:00.123456Z"

// fixedPSK is 32 zero bytes. NEVER reuse a real key in a test corpus.
var fixedPSK = bytes.Repeat([]byte{0}, 32)

type corpusCase struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	Envelope     map[string]any `json:"envelope"`
	CanonicalHex string         `json:"canonical_hex"`
	SigHex       string         `json:"sig_hex"`
	FramedHex    string         `json:"framed_hex"`
}

type corpus struct {
	Schema  string       `json:"schema"`
	PSKHex  string       `json:"psk_hex"`
	Comment string       `json:"comment"`
	Tests   []corpusCase `json:"tests"`
}

func main() {
	cases := []struct {
		name string
		desc string
		env  *arcp.Envelope
	}{
		{
			name: "ping",
			desc: "minimal ping with empty payload and no optional fields",
			env: func() *arcp.Envelope {
				e := arcp.New("msg_01habcdef1234567890abcdef", arcp.TypePing, fixedTimestamp)
				return e
			}(),
		},
		{
			name: "session.open",
			desc: "client capability negotiation; nested payload",
			env: func() *arcp.Envelope {
				e := arcp.New("msg_01session1234567890abcdef", arcp.TypeSessionOpen, fixedTimestamp)
				e.TraceID = "tr_01trace0000000000000000ab"
				e.Payload = map[string]any{
					"capabilities": map[string]any{
						"agent_handoff":  false,
						"binary_streams": true,
						"checkpoints":    false,
						"durable_jobs":   false,
						"streaming":      true,
					},
					"client": map[string]any{
						"name":    "xpc",
						"version": "0.0.0-dev",
					},
				}
				return e
			}(),
		},
		{
			name: "tool.invoke.exec",
			desc: "tool invoke with nested arguments",
			env: func() *arcp.Envelope {
				e := arcp.New("msg_01invoke12345678901234ab", arcp.TypeToolInvoke, fixedTimestamp)
				e.SessionID = "sess_01session1234567890abcdef"
				e.TraceID = "tr_01trace0000000000000000ab"
				e.Payload = map[string]any{
					"tool": "exec",
					"arguments": map[string]any{
						"cmd":     "dir 'C:\\'",
						"shell":   "cmd",
						"timeout": float64(30),
					},
				}
				return e
			}(),
		},
		{
			name: "stream.chunk.text",
			desc: "text stream chunk with stream_id and correlation_id",
			env: func() *arcp.Envelope {
				e := arcp.New("msg_01chunk00000000000000ab", arcp.TypeStreamChunk, fixedTimestamp)
				e.SessionID = "sess_01session1234567890abcdef"
				e.JobID = "job_01job12345678901234567a"
				e.StreamID = "str_01stream0000000000000ab"
				e.CorrelationID = "msg_01invoke12345678901234ab"
				e.Payload = map[string]any{
					"delta": "Volume in drive C is...\r\n",
				}
				return e
			}(),
		},
		{
			name: "tool.error",
			desc: "structured tool error with retryable flag",
			env: func() *arcp.Envelope {
				e := arcp.New("msg_01error000000000000000a", arcp.TypeToolError, fixedTimestamp)
				e.SessionID = "sess_01session1234567890abcdef"
				e.JobID = "job_01job12345678901234567a"
				e.CorrelationID = "msg_01invoke12345678901234ab"
				e.Payload = map[string]any{
					"code":      "EXEC_FAILED",
					"message":   "exit code 1",
					"retryable": false,
				}
				return e
			}(),
		},
		{
			name: "tools.list",
			desc: "client requests the agent's tool catalog",
			env: func() *arcp.Envelope {
				e := arcp.New("msg_01toolslist00000000000a", arcp.TypeToolsList, fixedTimestamp)
				e.SessionID = "sess_01session1234567890abcdef"
				e.TraceID = "tr_01trace0000000000000000ab"
				return e
			}(),
		},
		{
			name: "tools.list.result",
			desc: "agent returns descriptors for its registered tools",
			env: func() *arcp.Envelope {
				e := arcp.New("msg_01toolsresult000000000a", arcp.TypeToolsResult, fixedTimestamp)
				e.SessionID = "sess_01session1234567890abcdef"
				e.CorrelationID = "msg_01toolslist00000000000a"
				e.TraceID = "tr_01trace0000000000000000ab"
				e.Payload = map[string]any{
					"tools": []any{
						map[string]any{
							"name":        "exec",
							"description": "Run a command on the VM and stream stdout/stderr.",
							"input_schema": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"cmd":     map[string]any{"type": "string"},
									"shell":   map[string]any{"type": "string", "enum": []any{"cmd", "python", "python_file"}},
									"timeout": map[string]any{"type": "integer", "minimum": float64(0)},
								},
								"required": []any{"cmd"},
							},
						},
					},
				}
				return e
			}(),
		},
		{
			name: "html.special.chars",
			desc: "payload containing < > & to test no-HTML-escape parity",
			env: func() *arcp.Envelope {
				e := arcp.New("msg_01htmlchar0000000000ab", arcp.TypeLog, fixedTimestamp)
				e.Payload = map[string]any{
					"level":   "info",
					"message": "<rendered & 'safe'>",
				}
				return e
			}(),
		},
	}

	out := corpus{
		Schema:  "xpc.protocol.corpus.v1",
		PSKHex:  hex.EncodeToString(fixedPSK),
		Comment: "Generated by cmd/gen-corpus. Do not edit by hand. Re-run after protocol changes.",
		Tests:   make([]corpusCase, 0, len(cases)),
	}

	for _, c := range cases {
		// Sign in place to populate auth.sig.
		if err := arcp.Sign(c.env, fixedPSK); err != nil {
			fail("sign %s: %v", c.name, err)
		}

		// Compute the canonical signing bytes (sig blanked) for the corpus.
		canonical, err := canonicalForCorpus(c.env)
		if err != nil {
			fail("canonical %s: %v", c.name, err)
		}

		// Framed wire bytes are the length-prefixed marshal of the signed envelope.
		var framed bytes.Buffer
		if err := arcp.WriteFrame(&framed, c.env); err != nil {
			fail("write frame %s: %v", c.name, err)
		}

		// Build the envelope dict to embed in JSON. Strip auth.sig for the
		// "input" view; consumers re-sign with fixed_psk and check against
		// sig_hex.
		envMap, err := envelopeToMap(c.env)
		if err != nil {
			fail("env to map %s: %v", c.name, err)
		}

		out.Tests = append(out.Tests, corpusCase{
			Name:         c.name,
			Description:  c.desc,
			Envelope:     envMap,
			CanonicalHex: hex.EncodeToString(canonical),
			SigHex:       c.env.Auth.Sig,
			FramedHex:    hex.EncodeToString(framed.Bytes()),
		})
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		fail("encode: %v", err)
	}
}

func canonicalForCorpus(e *arcp.Envelope) ([]byte, error) {
	// We want the canonical bytes that HMAC was computed over. The package
	// exposes Sign/VerifySig but not the helper directly. Re-derive by
	// blanking auth.sig and re-marshaling with the same canonical rules.
	clone := *e
	clone.Auth.Sig = ""
	return clone.Marshal()
}

func envelopeToMap(e *arcp.Envelope) (map[string]any, error) {
	b, err := e.Marshal()
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	// Blank out the sig in the "envelope" view so consumers must re-sign.
	if auth, ok := m["auth"].(map[string]any); ok {
		auth["sig"] = ""
	}
	return m, nil
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gen-corpus: "+format+"\n", args...)
	os.Exit(1)
}
