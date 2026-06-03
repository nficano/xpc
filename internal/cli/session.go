package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/nficano/xpc/internal/arcp"
	"github.com/nficano/xpc/internal/profile"
	"github.com/nficano/xpc/internal/transport"
)

// requireDialable validates that p carries the minimum config to open a
// session (host, pinned fingerprint, PSK). It returns a usage/auth error with
// remediation guidance — these are permanent misconfigurations, distinct from
// a transient failure to reach a correctly-configured VM.
func requireDialable(p *profile.Profile) error {
	if p.Host == "" {
		return wrapUsage(fmt.Errorf("profile %q has no host; run `xpc configure --profile %s` or `xpc bootstrap`", p.Name, p.Name))
	}
	if p.Fingerprint == "" {
		return wrapUsage(fmt.Errorf("profile %q has no pinned fingerprint; run `xpc bootstrap --profile %s`", p.Name, p.Name))
	}
	if len(p.PSK) == 0 {
		return wrapAuth(fmt.Errorf("profile %q has no PSK in ~/.xpc/credentials; run `xpc bootstrap --profile %s`", p.Name, p.Name))
	}
	return nil
}

// dialAndOpen connects to the agent named by p, performs TLS+session.open, and
// returns the live conn plus the resolved session_id.
func dialAndOpen(p *profile.Profile, dialTimeout time.Duration) (net.Conn, string, error) {
	if err := requireDialable(p); err != nil {
		return nil, "", err
	}
	if dialTimeout <= 0 {
		dialTimeout = 10 * time.Second
	}
	addr := fmt.Sprintf("%s:%d", p.Host, p.Port)
	conn, err := transport.Dial(addr, p.Fingerprint, dialTimeout)
	if err != nil {
		return nil, "", wrapConnection(err)
	}

	openEnv := arcp.New(arcp.MustNewID(arcp.PrefixMessage), arcp.TypeSessionOpen, arcp.FormatTimestamp(time.Now()))
	openEnv.TraceID = arcp.MustNewID(arcp.PrefixTrace)
	openEnv.Payload = map[string]any{
		"client":       map[string]any{"name": "xpc", "version": "0.0.0-dev"},
		"capabilities": map[string]any{"streaming": true, "binary_streams": true},
	}
	if err := arcp.Sign(openEnv, p.PSK); err != nil {
		_ = conn.Close()
		return nil, "", err
	}
	if err := arcp.WriteFrame(conn, openEnv); err != nil {
		_ = conn.Close()
		return nil, "", wrapConnection(err)
	}

	resp, err := readSignedFrame(conn, p.PSK, 10*time.Second)
	if err != nil {
		_ = conn.Close()
		return nil, "", err
	}
	if resp.Type != arcp.TypeSessionAccepted {
		_ = conn.Close()
		return nil, "", fmt.Errorf("expected session.accepted; got %s", resp.Type)
	}
	sid := resp.SessionID
	if sid == "" {
		if v, ok := resp.Payload["session_id"].(string); ok {
			sid = v
		}
	}
	return conn, sid, nil
}

// readSignedFrame reads one envelope and verifies its HMAC. It applies a
// deadline so a wedged agent can't hang the CLI indefinitely.
func readSignedFrame(conn net.Conn, psk []byte, timeout time.Duration) (*arcp.Envelope, error) {
	if dl, ok := conn.(interface{ SetReadDeadline(time.Time) error }); ok && timeout > 0 {
		_ = dl.SetReadDeadline(time.Now().Add(timeout))
		defer func() { _ = dl.SetReadDeadline(time.Time{}) }()
	}
	env, err := arcp.ReadFrame(conn)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, wrapConnection(err)
		}
		return nil, wrapConnection(err)
	}
	if err := arcp.VerifySig(env, psk); err != nil {
		return nil, wrapAuth(err)
	}
	return env, nil
}

// invokeExec drives a tool.invoke exec round-trip: send invoke, pump stream
// chunks to stdout/stderr writers, return the remote exit code (or non-zero
// sentinel on timeout / error).
func invokeExec(
	ctx context.Context,
	conn net.Conn,
	psk []byte,
	sessionID, traceID, cmd, shell string,
	timeoutSec int,
	stdoutW, stderrW io.Writer,
) (int, error) {
	invoke := arcp.New(arcp.MustNewID(arcp.PrefixMessage), arcp.TypeToolInvoke, arcp.FormatTimestamp(time.Now()))
	invoke.SessionID = sessionID
	invoke.TraceID = traceID
	args := map[string]any{"cmd": cmd, "shell": shell}
	if timeoutSec > 0 {
		args["timeout"] = timeoutSec
	}
	invoke.Payload = map[string]any{"tool": "exec", "arguments": args}
	if err := arcp.Sign(invoke, psk); err != nil {
		return 0, err
	}
	if err := arcp.WriteFrame(conn, invoke); err != nil {
		return 0, wrapConnection(err)
	}

	streamChannels := map[string]string{}
	exitCode := -1
	timedOut := false

	for {
		env, err := readSignedFrame(conn, psk, 0)
		if err != nil {
			return 0, err
		}
		switch env.Type {
		case arcp.TypeJobAccepted, arcp.TypeJobStarted:
			// informational
		case arcp.TypeStreamOpen:
			ch, _ := env.Payload["channel"].(string)
			streamChannels[env.StreamID] = ch
		case arcp.TypeStreamChunk:
			delta, _ := env.Payload["delta"].(string)
			if streamChannels[env.StreamID] == "stderr" {
				_, _ = stderrW.Write([]byte(delta))
			} else {
				_, _ = stdoutW.Write([]byte(delta))
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
		case arcp.TypeJobCompleted:
			if timedOut {
				return 124, nil
			}
			if exitCode == -1 {
				return 0, fmt.Errorf("missing tool.result before job.completed")
			}
			return exitCode, nil
		case arcp.TypeJobFailed:
			return 1, fmt.Errorf("job failed")
		case arcp.TypeJobCancelled:
			return 130, fmt.Errorf("job cancelled")
		case arcp.TypeToolError:
			code, _ := env.Payload["code"].(string)
			msg, _ := env.Payload["message"].(string)
			return 0, &RemoteError{error: fmt.Errorf("%s: %s", code, msg), ExitCode: 5}
		case arcp.TypeNack:
			code, _ := env.Payload["code"].(string)
			msg, _ := env.Payload["message"].(string)
			return 0, fmt.Errorf("nack: %s: %s", code, msg)
		}
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
	}
}

// closeSession sends a session.close envelope. Best-effort.
func closeSession(conn net.Conn, psk []byte, sessionID string) {
	closeEnv := arcp.New(arcp.MustNewID(arcp.PrefixMessage), arcp.TypeSessionClose, arcp.FormatTimestamp(time.Now()))
	closeEnv.SessionID = sessionID
	closeEnv.Payload = map[string]any{"reason": "client_done"}
	if err := arcp.Sign(closeEnv, psk); err == nil {
		_ = arcp.WriteFrame(conn, closeEnv)
	}
}
