// Command xpc-roundtrip is the Phase 3 real-VM round-trip client.
//
// It dials a Phase 3 echo server (see agent/scripts/echo_server.py),
// performs a TLS 1.2 handshake with fingerprint pinning, then sends one
// envelope of each major v0 message type. The echo server replies with the
// same envelope, with id and type suffixed by ".echo". This client verifies
// every echo and reports OK/FAIL.
//
// Run after Phase 3 deploy:
//
//	go run ./cmd/xpc-roundtrip \
//	    --addr xp-truvoice-w02:9579 \
//	    --fingerprint <sha256 hex> \
//	    --psk /path/to/psk.hex
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/nficano/xpc/internal/arcp"
	"github.com/nficano/xpc/internal/transport"
)

func main() {
	var (
		addr        = flag.String("addr", "xp-truvoice-w02:9579", "echo server addr (host:port)")
		fingerprint = flag.String("fingerprint", "", "expected sha256 fingerprint of the server cert (hex, optionally sha256:AB:CD: prefixed)")
		pskFile     = flag.String("psk", "", "path to a hex-encoded 32-byte PSK file")
		timeout     = flag.Duration("timeout", 10*time.Second, "per-step timeout")
	)
	flag.Parse()

	if *fingerprint == "" || *pskFile == "" {
		fmt.Fprintln(os.Stderr, "usage: xpc-roundtrip --addr H:P --fingerprint HEX --psk FILE")
		os.Exit(2)
	}

	psk, err := loadPSK(*pskFile)
	if err != nil {
		log.Fatalf("psk: %v", err)
	}

	conn, err := transport.Dial(*addr, *fingerprint, *timeout)
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	fmt.Printf("CONNECTED %s (TLS 1.2)\n", *addr)

	// Each test sends one envelope and verifies the echo. Use a representative
	// subset of v0 message types, matching the corpus.
	cases := []struct {
		name    string
		msgType string
		mutate  func(*arcp.Envelope)
	}{
		{"ping", arcp.TypePing, nil},
		{"session.open", arcp.TypeSessionOpen, func(e *arcp.Envelope) {
			e.Payload["capabilities"] = map[string]interface{}{
				"streaming":      true,
				"binary_streams": true,
			}
		}},
		{"tool.invoke", arcp.TypeToolInvoke, func(e *arcp.Envelope) {
			e.Payload["tool"] = "exec"
			e.Payload["arguments"] = map[string]interface{}{
				"cmd": "dir 'C:\\'",
			}
		}},
		{"stream.chunk", arcp.TypeStreamChunk, func(e *arcp.Envelope) {
			e.StreamID = "str_phase3roundtripverify00"
			e.Payload["delta"] = "Volume in drive C is...\r\n"
		}},
		{"log", arcp.TypeLog, func(e *arcp.Envelope) {
			e.Payload["level"] = "info"
			e.Payload["message"] = "<rendered & 'safe'>"
		}},
	}

	failures := 0
	for _, c := range cases {
		id := arcp.MustNewID(arcp.PrefixMessage)
		ts := arcp.FormatTimestamp(time.Now())
		e := arcp.New(id, c.msgType, ts)
		if c.mutate != nil {
			c.mutate(e)
		}
		if err := arcp.Sign(e, psk); err != nil {
			fmt.Printf("FAIL %s: sign: %v\n", c.name, err)
			failures++
			continue
		}
		if err := arcp.WriteFrame(conn, e); err != nil {
			fmt.Printf("FAIL %s: write: %v\n", c.name, err)
			failures++
			continue
		}
		echo, err := arcp.ReadFrame(conn)
		if err != nil {
			fmt.Printf("FAIL %s: read: %v\n", c.name, err)
			failures++
			continue
		}
		if err := arcp.VerifySig(echo, psk); err != nil {
			fmt.Printf("FAIL %s: verify echo sig: %v\n", c.name, err)
			failures++
			continue
		}
		if echo.ID != e.ID+".echo" {
			fmt.Printf("FAIL %s: echo id mismatch: %s vs %s\n", c.name, echo.ID, e.ID+".echo")
			failures++
			continue
		}
		if echo.Type != e.Type+".echo" {
			fmt.Printf("FAIL %s: echo type mismatch: %s vs %s.echo\n", c.name, echo.Type, e.Type)
			failures++
			continue
		}
		fmt.Printf("OK   %-12s -> %s\n", c.name, echo.Type)
	}

	if failures > 0 {
		fmt.Printf("\nROUND-TRIP FAILED: %d/%d cases failed\n", failures, len(cases))
		os.Exit(1)
	}
	fmt.Printf("\nROUND-TRIP OK (%d/%d cases)\n", len(cases), len(cases))
}

func loadPSK(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	hexStr := strings.TrimSpace(string(raw))
	psk, err := hex.DecodeString(hexStr)
	if err != nil {
		return nil, fmt.Errorf("hex decode: %w", err)
	}
	if len(psk) != 32 {
		return nil, fmt.Errorf("psk must be 32 bytes; got %d", len(psk))
	}
	return psk, nil
}
