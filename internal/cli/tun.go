package cli

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/nficano/xpc/internal/arcp"
)

// xpc tun -L <localPort>:<vmHost>:<vmPort>
//
// Opens a local TCP listener; each accepted connection invokes the agent's
// tun.connect tool, then bidirectionally pipes bytes through ARCP streams
// (delta_b64 stream.chunks). Loosely modeled on `ssh -L`.

func newTunCmd(g *Globals) *cobra.Command {
	var (
		localSpec   string
		reverseSpec string
		idleTimeout time.Duration
	)
	c := &cobra.Command{
		Use:   "tun",
		Short: "Forward local TCP through the agent: -L localPort:vmHost:vmPort.",
		Long: `Opens a local TCP listener on localPort. Each accepted connection
triggers a fresh ARCP session; the agent's tun.connect tool opens TCP to
vmHost:vmPort on the VM-side network and shuttles bytes in both directions
via stream.chunk envelopes (delta_b64 binary frames).

Examples:
  # Forward a local port to xpc agent itself (TLS pinned-fingerprint test)
  xpc tun -L 19579:127.0.0.1:9579

  # Forward to an HTTP server running on the VM
  xpc tun -L 8080:127.0.0.1:80
`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if reverseSpec != "" && localSpec != "" {
				return wrapUsage(fmt.Errorf("--local (-L) and --remote (-R) cannot be combined"))
			}
			if reverseSpec != "" {
				return runTunReverse(cmd, g, reverseSpec, idleTimeout)
			}
			if localSpec == "" {
				return wrapUsage(fmt.Errorf("--local (-L) or --remote (-R) is required"))
			}
			localPort, vmHost, vmPort, err := parseTunSpec(localSpec)
			if err != nil {
				return wrapUsage(err)
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			lis, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
			if err != nil {
				return fmt.Errorf("listen 127.0.0.1:%d: %w", localPort, err)
			}
			defer func() { _ = lis.Close() }()
			cmd.Printf("xpc tun: 127.0.0.1:%d -> %s:%d (Ctrl-C to stop)\n", localPort, vmHost, vmPort)

			// Stop the listener on context done.
			go func() {
				<-ctx.Done()
				_ = lis.Close()
			}()

			for {
				conn, err := lis.Accept()
				if err != nil {
					if ctx.Err() != nil {
						return nil
					}
					return fmt.Errorf("accept: %w", err)
				}
				go handleTunConn(ctx, g, conn, vmHost, vmPort, idleTimeout, cmd)
			}
		},
	}
	c.Flags().StringVarP(&localSpec, "local", "L", "", "Forward localPort:vmHost:vmPort (mirrors ssh -L)")
	c.Flags().StringVarP(&reverseSpec, "remote", "R", "", "Reverse forward vmPort:hostHost:hostPort (mirrors ssh -R)")
	c.Flags().DurationVar(&idleTimeout, "idle-timeout", 0, "Drop forwards idle for this long (0 = never)")
	return c
}

// runTunReverse implements `xpc tun -R vmPort:hostHost:hostPort`.
//
// Invokes the agent's tun.reverse tool to open a VM-side listener.
// For each accepted VM connection, the agent emits stream.open with
// channel="reverse_up" and a fresh conn_id; this side responds with
// stream.open channel="reverse_down" carrying the same conn_id, then
// shuttles bytes between the agent's upstream and a fresh local TCP
// connection to hostHost:hostPort.
func runTunReverse(cmd *cobra.Command, g *Globals, spec string, idleTimeout time.Duration) error {
	vmPort, hostHost, hostPort, err := parseTunSpec(spec)
	if err != nil {
		return wrapUsage(fmt.Errorf("--remote: %w", err))
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	p, err := g.ResolveProfile()
	if err != nil {
		return err
	}
	tlsConn, sessionID, err := dialAndOpen(p, g.Timeout)
	if err != nil {
		return err
	}
	defer func() {
		closeSession(tlsConn, p.PSK, sessionID)
		_ = tlsConn.Close()
	}()

	var writeMu sync.Mutex
	send := func(env *arcp.Envelope) error {
		if err := arcp.Sign(env, p.PSK); err != nil {
			return err
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		return arcp.WriteFrame(tlsConn, env)
	}

	traceID := arcp.MustNewID(arcp.PrefixTrace)
	invoke := arcp.New(arcp.MustNewID(arcp.PrefixMessage), arcp.TypeToolInvoke, arcp.FormatTimestamp(time.Now()))
	invoke.SessionID = sessionID
	invoke.TraceID = traceID
	invoke.Payload = map[string]any{
		"tool":      "tun.reverse",
		"arguments": map[string]any{"listen_port": vmPort},
	}
	if err := send(invoke); err != nil {
		return wrapConnection(err)
	}

	cmd.Printf("xpc tun -R: VM:%d -> %s:%d (Ctrl-C to stop)\n", vmPort, hostHost, hostPort)

	type revConn struct {
		local        net.Conn
		downStreamID string
	}
	var (
		jobID    string
		mu       sync.Mutex
		conns    = map[string]*revConn{} // upStreamID -> revConn
		downByUp = map[string]string{}   // upStreamID -> downStreamID (echo for routing)
	)

	openLocalAndStart := func(connID, upStreamID string) {
		local, err := net.Dial("tcp", net.JoinHostPort(hostHost, strconv.Itoa(hostPort)))
		if err != nil {
			cmd.PrintErrf("tun -R: dial %s:%d for conn_id=%s: %v\n", hostHost, hostPort, connID, err)
			closeEv := arcp.New(arcp.MustNewID(arcp.PrefixMessage),
				arcp.TypeStreamClose, arcp.FormatTimestamp(time.Now()))
			closeEv.SessionID = sessionID
			closeEv.TraceID = traceID
			closeEv.JobID = jobID
			closeEv.StreamID = upStreamID
			closeEv.Payload = map[string]any{"reason": "host_dial_failed", "conn_id": connID}
			_ = send(closeEv)
			return
		}
		downStreamID := arcp.MustNewID(arcp.PrefixStream)
		mu.Lock()
		conns[upStreamID] = &revConn{local: local, downStreamID: downStreamID}
		downByUp[upStreamID] = downStreamID
		mu.Unlock()

		// Tell the agent which stream id we're going to push host->VM bytes on.
		openEv := arcp.New(arcp.MustNewID(arcp.PrefixMessage),
			arcp.TypeStreamOpen, arcp.FormatTimestamp(time.Now()))
		openEv.SessionID = sessionID
		openEv.TraceID = traceID
		openEv.JobID = jobID
		openEv.StreamID = downStreamID
		openEv.Payload = map[string]any{"channel": "reverse_down", "conn_id": connID}
		if err := send(openEv); err != nil {
			cmd.PrintErrf("tun -R: send stream.open: %v\n", err)
			_ = local.Close()
			return
		}

		// Pump local -> agent.
		go func() {
			buf := make([]byte, 8*1024)
			for {
				if idleTimeout > 0 {
					_ = local.SetReadDeadline(time.Now().Add(idleTimeout))
				}
				n, err := local.Read(buf)
				if n > 0 {
					chunk := arcp.New(arcp.MustNewID(arcp.PrefixMessage),
						arcp.TypeStreamChunk, arcp.FormatTimestamp(time.Now()))
					chunk.SessionID = sessionID
					chunk.TraceID = traceID
					chunk.JobID = jobID
					chunk.StreamID = downStreamID
					chunk.Payload = map[string]any{
						"delta_b64": base64.StdEncoding.EncodeToString(buf[:n]),
					}
					if serr := send(chunk); serr != nil {
						return
					}
				}
				if err != nil {
					closeEv := arcp.New(arcp.MustNewID(arcp.PrefixMessage),
						arcp.TypeStreamClose, arcp.FormatTimestamp(time.Now()))
					closeEv.SessionID = sessionID
					closeEv.TraceID = traceID
					closeEv.JobID = jobID
					closeEv.StreamID = downStreamID
					closeEv.Payload = map[string]any{"reason": "host_eof", "conn_id": connID}
					_ = send(closeEv)
					return
				}
			}
		}()
	}

	closed := make(chan struct{})
	readerErr := make(chan error, 1)
	go func() {
		defer close(closed)
		for {
			env, err := arcp.ReadFrame(tlsConn)
			if err != nil {
				if !errors.Is(err, io.EOF) {
					readerErr <- err
				}
				return
			}
			if err := arcp.VerifySig(env, p.PSK); err != nil {
				readerErr <- fmt.Errorf("verify: %w", err)
				return
			}
			switch env.Type {
			case arcp.TypeJobAccepted:
				jobID = env.JobID
			case arcp.TypeJobStarted:
				// informational
			case arcp.TypeStreamOpen:
				ch, _ := env.Payload["channel"].(string)
				if ch != "reverse_up" {
					continue
				}
				connID, _ := env.Payload["conn_id"].(string)
				if connID == "" {
					continue
				}
				openLocalAndStart(connID, env.StreamID)
			case arcp.TypeStreamChunk:
				mu.Lock()
				rc := conns[env.StreamID]
				mu.Unlock()
				if rc == nil {
					continue
				}
				delta, _ := env.Payload["delta_b64"].(string)
				if delta == "" {
					continue
				}
				raw, dec := base64.StdEncoding.DecodeString(delta)
				if dec != nil {
					readerErr <- fmt.Errorf("decode upstream delta_b64: %w", dec)
					return
				}
				if _, werr := rc.local.Write(raw); werr != nil {
					_ = rc.local.Close()
				}
			case arcp.TypeStreamClose, arcp.TypeStreamError:
				mu.Lock()
				rc := conns[env.StreamID]
				delete(conns, env.StreamID)
				delete(downByUp, env.StreamID)
				mu.Unlock()
				if rc != nil {
					if cw, ok := rc.local.(interface{ CloseWrite() error }); ok {
						_ = cw.CloseWrite()
					} else {
						_ = rc.local.Close()
					}
				}
			case arcp.TypeJobCompleted, arcp.TypeJobFailed, arcp.TypeJobCancelled:
				return
			case arcp.TypeToolError:
				code, _ := env.Payload["code"].(string)
				msg, _ := env.Payload["message"].(string)
				readerErr <- fmt.Errorf("tun.reverse error %s: %s", code, msg)
				return
			case arcp.TypeNack:
				code, _ := env.Payload["code"].(string)
				msg, _ := env.Payload["message"].(string)
				readerErr <- fmt.Errorf("nack %s: %s", code, msg)
				return
			}
		}
	}()

	select {
	case <-closed:
	case err := <-readerErr:
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
	case <-ctx.Done():
		if jobID != "" {
			cancelEv := arcp.New(arcp.MustNewID(arcp.PrefixMessage),
				arcp.TypeCancel, arcp.FormatTimestamp(time.Now()))
			cancelEv.SessionID = sessionID
			cancelEv.JobID = jobID
			cancelEv.Payload = map[string]any{"job_id": jobID}
			_ = send(cancelEv)
		}
	}
	mu.Lock()
	for _, rc := range conns {
		_ = rc.local.Close()
	}
	mu.Unlock()
	return nil
}

func parseTunSpec(s string) (int, string, int, error) {
	parts := strings.SplitN(s, ":", 3)
	if len(parts) != 3 {
		return 0, "", 0, fmt.Errorf("expected localPort:vmHost:vmPort, got %q", s)
	}
	lp, err := strconv.Atoi(parts[0])
	if err != nil || lp <= 0 || lp > 65535 {
		return 0, "", 0, fmt.Errorf("local port must be 1-65535: %q", parts[0])
	}
	rp, err := strconv.Atoi(parts[2])
	if err != nil || rp <= 0 || rp > 65535 {
		return 0, "", 0, fmt.Errorf("vm port must be 1-65535: %q", parts[2])
	}
	if parts[1] == "" {
		return 0, "", 0, fmt.Errorf("vm host must not be empty")
	}
	return lp, parts[1], rp, nil
}

// handleTunConn services one accepted local connection. It:
//  1. opens a session + tool.invoke tun.connect
//  2. spawns a reader for the TLS conn that routes incoming envelopes
//  3. spawns a forwarder for local->agent (stream.chunk envelopes)
//  4. shuts everything down on either side closing
func handleTunConn(ctx context.Context, g *Globals, local net.Conn, vmHost string, vmPort int, idleTimeout time.Duration, cmd *cobra.Command) {
	defer func() { _ = local.Close() }()

	p, err := g.ResolveProfile()
	if err != nil {
		cmd.PrintErrln("tun: profile:", err)
		return
	}
	tlsConn, sessionID, err := dialAndOpen(p, g.Timeout)
	if err != nil {
		cmd.PrintErrln("tun: dial:", err)
		return
	}
	defer func() {
		closeSession(tlsConn, p.PSK, sessionID)
		_ = tlsConn.Close()
	}()

	// Single mutex for outbound writes -- multiple goroutines write
	// envelopes (the local-reader pump + the cancel sender).
	var writeMu sync.Mutex
	send := func(env *arcp.Envelope) error {
		if err := arcp.Sign(env, p.PSK); err != nil {
			return err
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		return arcp.WriteFrame(tlsConn, env)
	}

	// Send tool.invoke tun.connect.
	traceID := arcp.MustNewID(arcp.PrefixTrace)
	invokeID := arcp.MustNewID(arcp.PrefixMessage)
	invoke := arcp.New(invokeID, arcp.TypeToolInvoke, arcp.FormatTimestamp(time.Now()))
	invoke.SessionID = sessionID
	invoke.TraceID = traceID
	invoke.Payload = map[string]any{
		"tool": "tun.connect",
		"arguments": map[string]any{
			"host": vmHost,
			"port": vmPort,
		},
	}
	if err := send(invoke); err != nil {
		cmd.PrintErrln("tun: invoke:", err)
		return
	}

	// Allocate a downstream stream id for our local->agent direction.
	downstreamID := arcp.MustNewID(arcp.PrefixStream)

	type runState struct {
		jobID   string
		gotOpen bool
		// upstreamID is set when stream.open arrives from the agent.
		upstreamID string
		// closed signals the reader has hit a terminal envelope.
		closed chan struct{}
		// jobReady fires once st.jobID is populated. The forwarder waits
		// on it so its first stream.chunk doesn't go out with an empty
		// job_id (which the agent silently drops).
		jobReady chan string
	}
	st := &runState{
		closed:   make(chan struct{}),
		jobReady: make(chan string, 1),
	}

	// Reader goroutine: decode envelopes off the wire, dispatch.
	readerErr := make(chan error, 1)
	go func() {
		defer close(st.closed)
		for {
			env, err := arcp.ReadFrame(tlsConn)
			if err != nil {
				readerErr <- err
				return
			}
			if err := arcp.VerifySig(env, p.PSK); err != nil {
				readerErr <- fmt.Errorf("verify: %w", err)
				return
			}
			switch env.Type {
			case arcp.TypeJobAccepted:
				st.jobID = env.JobID
				select {
				case st.jobReady <- env.JobID:
				default:
				}
			case arcp.TypeJobStarted:
				// informational
			case arcp.TypeStreamOpen:
				st.upstreamID = env.StreamID
				st.gotOpen = true
			case arcp.TypeStreamChunk:
				if st.upstreamID == "" || env.StreamID != st.upstreamID {
					continue
				}
				if delta, ok := env.Payload["delta_b64"].(string); ok && delta != "" {
					raw, dec := base64.StdEncoding.DecodeString(delta)
					if dec != nil {
						readerErr <- fmt.Errorf("decode upstream delta_b64: %w", dec)
						return
					}
					if _, werr := local.Write(raw); werr != nil {
						readerErr <- werr
						return
					}
				}
			case arcp.TypeStreamClose, arcp.TypeStreamError:
				// Half-close the local conn so the local app sees EOF.
				if cw, ok := local.(interface{ CloseWrite() error }); ok {
					_ = cw.CloseWrite()
				}
			case arcp.TypeJobCompleted, arcp.TypeJobFailed, arcp.TypeJobCancelled:
				return
			case arcp.TypeToolError:
				code, _ := env.Payload["code"].(string)
				msg, _ := env.Payload["message"].(string)
				readerErr <- fmt.Errorf("tun.connect error %s: %s", code, msg)
				return
			case arcp.TypeNack:
				code, _ := env.Payload["code"].(string)
				msg, _ := env.Payload["message"].(string)
				readerErr <- fmt.Errorf("nack %s: %s", code, msg)
				return
			}
		}
	}()

	// Forwarder: local -> agent. Wait for the job_id before pumping bytes.
	go func() {
		var jobID string
		select {
		case jobID = <-st.jobReady:
		case <-st.closed:
			return
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
			return
		}
		buf := make([]byte, 8*1024)
		for {
			if idleTimeout > 0 {
				_ = local.SetReadDeadline(time.Now().Add(idleTimeout))
			}
			n, err := local.Read(buf)
			if n > 0 {
				chunk := arcp.New(arcp.MustNewID(arcp.PrefixMessage),
					arcp.TypeStreamChunk, arcp.FormatTimestamp(time.Now()))
				chunk.SessionID = sessionID
				chunk.JobID = jobID
				chunk.StreamID = downstreamID
				chunk.TraceID = traceID
				chunk.Payload = map[string]any{
					"delta_b64": base64.StdEncoding.EncodeToString(buf[:n]),
				}
				if serr := send(chunk); serr != nil {
					return
				}
			}
			if err != nil {
				// EOF or read error: tell the agent we're done sending.
				closeEv := arcp.New(arcp.MustNewID(arcp.PrefixMessage),
					arcp.TypeStreamClose, arcp.FormatTimestamp(time.Now()))
				closeEv.SessionID = sessionID
				closeEv.JobID = jobID
				closeEv.StreamID = downstreamID
				closeEv.TraceID = traceID
				closeEv.Payload = map[string]any{"reason": "host_eof"}
				_ = send(closeEv)
				return
			}
		}
	}()

	// Wait for the reader to terminate.
	select {
	case <-st.closed:
	case err := <-readerErr:
		if err != nil && err != io.EOF {
			cmd.PrintErrln("tun:", err)
		}
	case <-ctx.Done():
		// Cancel the job.
		if st.jobID != "" {
			cancel := arcp.New(arcp.MustNewID(arcp.PrefixMessage),
				arcp.TypeCancel, arcp.FormatTimestamp(time.Now()))
			cancel.SessionID = sessionID
			cancel.JobID = st.jobID
			cancel.Payload = map[string]any{"job_id": st.jobID}
			_ = send(cancel)
		}
	}
}
