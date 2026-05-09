// Command xpc-exec is the Phase 4 end-to-end verifier. It opens a session
// against an xpc agent, invokes the `exec` tool with a caller-supplied
// command, prints stdout/stderr streams as they arrive, and returns the
// remote exit code.
//
// Phase 5 replaces this with a proper `xpc exec` cobra subcommand that
// uses the same flow.
//
// Run:
//
//	go run ./cmd/xpc-exec --addr xp-truvoice-w02:9579 \
//	    --fingerprint <hex> --psk /path/psk.hex -- dir C:\
package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"github.com/nficano/xpc/internal/arcp"
	"github.com/nficano/xpc/internal/transport"
)

func main() {
	var (
		addr        = flag.String("addr", "xp-truvoice-w02:9578", "agent addr (host:port)")
		fingerprint = flag.String("fingerprint", "", "expected sha256 fingerprint (hex, optionally sha256:AB:CD:... format)")
		pskFile     = flag.String("psk", "", "path to a hex-encoded 32-byte PSK file")
		shell       = flag.String("shell", "cmd", "remote shell: cmd | python | python_file")
		dialTimeout = flag.Duration("dial-timeout", 10*time.Second, "TLS dial timeout")
		jobTimeout  = flag.Duration("timeout", 0, "per-invocation timeout (0 = no timeout, propagated to exec arguments.timeout in seconds)")
	)
	flag.Parse()

	if *fingerprint == "" || *pskFile == "" {
		fmt.Fprintln(os.Stderr, "usage: xpc-exec --addr H:P --fingerprint HEX --psk FILE [--shell ...] -- cmd args...")
		os.Exit(2)
	}
	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "no command supplied")
		os.Exit(2)
	}
	cmd := strings.Join(args, " ")

	psk, err := loadPSK(*pskFile)
	if err != nil {
		log.Fatalf("psk: %v", err)
	}

	conn, err := transport.Dial(*addr, *fingerprint, *dialTimeout)
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	rc, err := runExec(conn, psk, cmd, *shell, *jobTimeout)
	if err != nil {
		log.Fatalf("exec: %v", err)
	}
	os.Exit(rc)
}

func runExec(conn net.Conn, psk []byte, cmd, shell string, timeout time.Duration) (int, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Background reader: parse envelopes off the wire. We dispatch to text
	// stdout/stderr writers and capture the terminal envelopes for the main
	// goroutine to consume.
	envCh := make(chan *arcp.Envelope, 32)
	errCh := make(chan error, 1)
	go func() {
		defer close(envCh)
		for {
			env, err := arcp.ReadFrame(conn)
			if err != nil {
				if !errors.Is(err, io.EOF) {
					errCh <- err
				}
				return
			}
			if err := arcp.VerifySig(env, psk); err != nil {
				errCh <- fmt.Errorf("verify: %w", err)
				return
			}
			envCh <- env
		}
	}()

	// 1. session.open
	openEnv := arcp.New(
		arcp.MustNewID(arcp.PrefixMessage),
		arcp.TypeSessionOpen,
		arcp.FormatTimestamp(time.Now()),
	)
	openEnv.TraceID = arcp.MustNewID(arcp.PrefixTrace)
	openEnv.Payload = map[string]any{
		"client":       map[string]any{"name": "xpc-exec", "version": "0"},
		"capabilities": map[string]any{"streaming": true, "binary_streams": true},
	}
	if err := arcp.Sign(openEnv, psk); err != nil {
		return 0, fmt.Errorf("sign session.open: %w", err)
	}
	if err := arcp.WriteFrame(conn, openEnv); err != nil {
		return 0, fmt.Errorf("write session.open: %w", err)
	}

	accepted, err := waitFor(ctx, envCh, errCh, arcp.TypeSessionAccepted, 10*time.Second)
	if err != nil {
		return 0, fmt.Errorf("session.open: %w", err)
	}
	sessionID := accepted.SessionID
	if sessionID == "" {
		// Some agents put session_id only in payload; fall back.
		if pid, ok := accepted.Payload["session_id"].(string); ok {
			sessionID = pid
		}
	}

	// 2. tool.invoke exec
	invoke := arcp.New(
		arcp.MustNewID(arcp.PrefixMessage),
		arcp.TypeToolInvoke,
		arcp.FormatTimestamp(time.Now()),
	)
	invoke.SessionID = sessionID
	invoke.TraceID = openEnv.TraceID
	args := map[string]any{"cmd": cmd, "shell": shell}
	if timeout > 0 {
		args["timeout"] = int64(timeout.Seconds())
	}
	invoke.Payload = map[string]any{
		"tool":      "exec",
		"arguments": args,
	}
	if err := arcp.Sign(invoke, psk); err != nil {
		return 0, fmt.Errorf("sign tool.invoke: %w", err)
	}
	if err := arcp.WriteFrame(conn, invoke); err != nil {
		return 0, fmt.Errorf("write tool.invoke: %w", err)
	}

	// 3. consume envelopes until a terminal arrives.
	streamChannels := map[string]string{} // stream_id -> "stdout"|"stderr"
	exitCode := -1
	timedOut := false

	var gotJobAccepted bool

	deadline := time.Now().Add(120 * time.Second)
	if timeout > 0 {
		deadline = time.Now().Add(timeout + 30*time.Second)
	}

loop:
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return 0, fmt.Errorf("timed out waiting for terminal envelope")
		}
		var env *arcp.Envelope
		select {
		case env = <-envCh:
		case err := <-errCh:
			return 0, err
		case <-time.After(left):
			return 0, fmt.Errorf("timed out waiting for terminal envelope")
		}
		if env == nil {
			return 0, fmt.Errorf("connection closed before terminal envelope")
		}

		switch env.Type {
		case arcp.TypeJobAccepted:
			gotJobAccepted = true
		case arcp.TypeJobStarted:
			// no-op; informational.
		case arcp.TypeStreamOpen:
			channel, _ := env.Payload["channel"].(string)
			streamChannels[env.StreamID] = channel
		case arcp.TypeStreamChunk:
			delta, _ := env.Payload["delta"].(string)
			ch := streamChannels[env.StreamID]
			if ch == "stderr" {
				_, _ = os.Stderr.WriteString(delta)
			} else {
				_, _ = os.Stdout.WriteString(delta)
			}
		case arcp.TypeStreamClose, arcp.TypeStreamError:
			delete(streamChannels, env.StreamID)
		case arcp.TypeToolResult:
			if v, ok := env.Payload["exit_code"].(float64); ok {
				exitCode = int(v)
			}
			if v, ok := env.Payload["timed_out"].(bool); ok {
				timedOut = v
			}
		case arcp.TypeJobCompleted, arcp.TypeJobFailed, arcp.TypeJobCancelled:
			break loop
		case arcp.TypeToolError:
			code, _ := env.Payload["code"].(string)
			msg, _ := env.Payload["message"].(string)
			fmt.Fprintf(os.Stderr, "tool.error: %s: %s\n", code, msg)
		case arcp.TypeNack:
			code, _ := env.Payload["code"].(string)
			msg, _ := env.Payload["message"].(string)
			return 0, fmt.Errorf("nack: %s: %s", code, msg)
		}
	}

	if !gotJobAccepted {
		return 0, fmt.Errorf("job never accepted")
	}
	if timedOut {
		return 124, nil
	}
	if exitCode == -1 {
		return 0, fmt.Errorf("missing tool.result")
	}

	// Best-effort session.close.
	closeEnv := arcp.New(arcp.MustNewID(arcp.PrefixMessage), arcp.TypeSessionClose, arcp.FormatTimestamp(time.Now()))
	closeEnv.SessionID = sessionID
	closeEnv.Payload = map[string]any{"reason": "client_done"}
	if err := arcp.Sign(closeEnv, psk); err == nil {
		_ = arcp.WriteFrame(conn, closeEnv)
	}

	return exitCode, nil
}

func waitFor(ctx context.Context, envCh <-chan *arcp.Envelope, errCh <-chan error, want string, timeout time.Duration) (*arcp.Envelope, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case env := <-envCh:
			if env == nil {
				return nil, fmt.Errorf("connection closed waiting for %s", want)
			}
			if env.Type == want {
				return env, nil
			}
			if env.Type == arcp.TypeNack {
				code, _ := env.Payload["code"].(string)
				msg, _ := env.Payload["message"].(string)
				return nil, fmt.Errorf("nack: %s: %s", code, msg)
			}
		case err := <-errCh:
			return nil, err
		case <-deadline.C:
			return nil, fmt.Errorf("timed out waiting for %s", want)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func loadPSK(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	psk, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("hex decode: %w", err)
	}
	if len(psk) != 32 {
		return nil, fmt.Errorf("psk must be 32 bytes; got %d", len(psk))
	}
	return psk, nil
}
