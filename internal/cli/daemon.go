package cli

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/nficano/xpc/internal/arcp"
	"github.com/nficano/xpc/internal/profile"
)

// xpc daemon
//
// A long-lived host-side process that holds warm TLS+session connections
// per profile so the CLI doesn't pay the handshake cost on every command.
// IPC over a Unix socket at ~/.xpc/run/daemon.sock; one-line JSON requests,
// one-line JSON responses (plus an optional binary stdout/stderr stream).
//
// v0 supports the `exec` action (the most common in tight loops):
//
//   {"action": "exec", "profile": "lab", "args": {"cmd": "ver", "shell": "cmd"}}
//   -> streams stdout chunks, then a final {"exit_code": N}
//
// Subcommands:
//
//   xpc daemon start       run in the foreground (use & to background)
//   xpc daemon stop        signal the running daemon to exit
//   xpc daemon status      print pid + active profiles + per-profile session age
//   xpc daemon exec ...    one-shot test path: ask the daemon to run an exec
//
// The CLI doesn't auto-route through the daemon yet; that's a follow-up
// once the protocol is stable across a few real workloads.

const (
	daemonRelDir    = ".xpc/run"
	daemonRelSocket = ".xpc/run/daemon.sock"
	daemonRelPID    = ".xpc/run/daemon.pid"
)

func newDaemonCmd(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Host-side connection multiplex daemon (warm TLS sessions per profile).",
	}
	cmd.AddCommand(newDaemonStartCmd(g))
	cmd.AddCommand(newDaemonStopCmd(g))
	cmd.AddCommand(newDaemonStatusCmd(g))
	cmd.AddCommand(newDaemonExecCmd(g))
	return cmd
}

func daemonPaths() (sock, pidFile string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	sock = filepath.Join(home, daemonRelSocket)
	pidFile = filepath.Join(home, daemonRelPID)
	return sock, pidFile, nil
}

func newDaemonStartCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Run the daemon in the foreground.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			sock, pidFile, err := daemonPaths()
			if err != nil {
				return err
			}
			if existing := readPID(pidFile); existing > 0 && processAlive(existing) {
				return wrapUsage(fmt.Errorf("daemon already running (pid %d)", existing))
			}
			if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
				return fmt.Errorf("mkdir %s: %w", filepath.Dir(sock), err)
			}
			_ = os.Remove(sock) // stale socket from a crashed previous run
			lis, err := net.Listen("unix", sock)
			if err != nil {
				return fmt.Errorf("listen %s: %w", sock, err)
			}
			defer func() {
				_ = lis.Close()
				_ = os.Remove(sock)
				_ = os.Remove(pidFile)
			}()
			if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
				return fmt.Errorf("write pid: %w", err)
			}
			cmd.Printf("xpc daemon listening on %s (pid %d)\n", sock, os.Getpid())

			d := newDaemon(g)
			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()
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
					return err
				}
				go d.serve(ctx, conn)
			}
		},
	}
}

func newDaemonStopCmd(_ *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Signal the running daemon to exit.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, pidFile, err := daemonPaths()
			if err != nil {
				return err
			}
			pid := readPID(pidFile)
			if pid <= 0 {
				return wrapUsage(fmt.Errorf("no daemon pid file at %s", pidFile))
			}
			if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
				return fmt.Errorf("kill %d: %w", pid, err)
			}
			cmd.Printf("sent SIGTERM to pid %d\n", pid)
			return nil
		},
	}
}

func newDaemonStatusCmd(_ *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Print the daemon pid and socket path.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			sock, pidFile, err := daemonPaths()
			if err != nil {
				return err
			}
			pid := readPID(pidFile)
			if pid <= 0 || !processAlive(pid) {
				cmd.Println("daemon: not running")
				return nil
			}
			cmd.Printf("daemon: pid %d, socket %s\n", pid, sock)
			return nil
		},
	}
}

func newDaemonExecCmd(g *Globals) *cobra.Command {
	var shell string
	c := &cobra.Command{
		Use:   "exec -- <cmd> [args...]",
		Short: "One-shot exec routed through the running daemon (proves the IPC path).",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sock, _, err := daemonPaths()
			if err != nil {
				return err
			}
			conn, err := net.Dial("unix", sock)
			if err != nil {
				return wrapConnection(fmt.Errorf("connect %s (is the daemon running?): %w", sock, err))
			}
			defer func() { _ = conn.Close() }()

			p, err := g.ResolveProfile()
			if err != nil {
				return err
			}

			req := map[string]interface{}{
				"action":  "exec",
				"profile": p.Name,
				"args": map[string]interface{}{
					"cmd":   strings.Join(args, " "),
					"shell": shell,
				},
			}
			payload, _ := json.Marshal(req)
			if _, err := fmt.Fprintln(conn, string(payload)); err != nil {
				return err
			}

			r := bufio.NewReader(conn)
			var exitCode int
			for {
				line, err := r.ReadString('\n')
				if err != nil {
					return err
				}
				var msg map[string]interface{}
				if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &msg); err != nil {
					return err
				}
				if errStr, ok := msg["error"].(string); ok && errStr != "" {
					return fmt.Errorf("daemon error: %s", errStr)
				}
				if delta, ok := msg["stdout_b64"].(string); ok && delta != "" {
					raw, _ := base64.StdEncoding.DecodeString(delta)
					_, _ = cmd.OutOrStdout().Write(raw)
				}
				if delta, ok := msg["stderr_b64"].(string); ok && delta != "" {
					raw, _ := base64.StdEncoding.DecodeString(delta)
					_, _ = cmd.ErrOrStderr().Write(raw)
				}
				if v, ok := msg["exit_code"].(float64); ok {
					exitCode = int(v)
					if exitCode != 0 {
						return &RemoteError{
							error:    fmt.Errorf("remote exit code %d", exitCode),
							ExitCode: exitCode,
						}
					}
					return nil
				}
				if done, _ := msg["done"].(bool); done {
					return nil
				}
			}
		},
	}
	c.Flags().StringVar(&shell, "shell", "cmd", "Remote shell: cmd | python | python_file")
	return c
}

// ---- daemon implementation -------------------------------------------------

type daemon struct {
	g        *Globals
	mu       sync.Mutex
	sessions map[string]*daemonSession // profile name -> warm session
}

type daemonSession struct {
	conn      net.Conn
	psk       []byte
	sessionID string
	openedAt  time.Time
}

func newDaemon(g *Globals) *daemon {
	return &daemon{g: g, sessions: map[string]*daemonSession{}}
}

func (d *daemon) serve(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		var req map[string]interface{}
		if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &req); err != nil {
			writeDaemonError(conn, err)
			continue
		}
		action, _ := req["action"].(string)
		profileName, _ := req["profile"].(string)
		argsRaw, _ := req["args"].(map[string]interface{})
		switch action {
		case "exec":
			if err := d.handleExec(ctx, conn, profileName, argsRaw); err != nil {
				writeDaemonError(conn, err)
			}
		case "ping":
			_, _ = fmt.Fprintln(conn, `{"pong": true}`)
		default:
			writeDaemonError(conn, fmt.Errorf("unknown action %q", action))
		}
	}
}

func writeDaemonError(w net.Conn, err error) {
	payload, _ := json.Marshal(map[string]interface{}{"error": err.Error()})
	_, _ = fmt.Fprintln(w, string(payload))
}

func (d *daemon) handleExec(ctx context.Context, conn net.Conn, profileName string, args map[string]interface{}) error {
	if profileName == "" {
		profileName = profile.DefaultName
	}
	sess, err := d.session(profileName)
	if err != nil {
		return err
	}
	cmdStr, _ := args["cmd"].(string)
	shell, _ := args["shell"].(string)
	if shell == "" {
		shell = "cmd"
	}

	// Send tool.invoke exec on the warm session.
	invoke := arcp.New(arcp.MustNewID(arcp.PrefixMessage), arcp.TypeToolInvoke,
		arcp.FormatTimestamp(time.Now()))
	invoke.SessionID = sess.sessionID
	invoke.Payload = map[string]interface{}{
		"tool": "exec",
		"arguments": map[string]interface{}{
			"cmd":   cmdStr,
			"shell": shell,
		},
	}
	if err := arcp.Sign(invoke, sess.psk); err != nil {
		return err
	}
	if err := arcp.WriteFrame(sess.conn, invoke); err != nil {
		// Session likely dead; drop and let the next call reopen.
		d.dropSession(profileName)
		return wrapConnection(err)
	}

	streamChannels := map[string]string{}
	for {
		env, err := arcp.ReadFrame(sess.conn)
		if err != nil {
			d.dropSession(profileName)
			return err
		}
		if err := arcp.VerifySig(env, sess.psk); err != nil {
			d.dropSession(profileName)
			return err
		}
		switch env.Type {
		case arcp.TypeJobAccepted, arcp.TypeJobStarted:
			// informational
		case arcp.TypeStreamOpen:
			ch, _ := env.Payload["channel"].(string)
			streamChannels[env.StreamID] = ch
		case arcp.TypeStreamChunk:
			delta, _ := env.Payload["delta"].(string)
			ch := streamChannels[env.StreamID]
			key := "stdout_b64"
			if ch == "stderr" {
				key = "stderr_b64"
			}
			out := map[string]interface{}{
				key: base64.StdEncoding.EncodeToString([]byte(delta)),
			}
			payload, _ := json.Marshal(out)
			_, _ = fmt.Fprintln(conn, string(payload))
		case arcp.TypeStreamClose, arcp.TypeStreamError:
			delete(streamChannels, env.StreamID)
		case arcp.TypeToolResult:
			rc := 0
			if v, ok := env.Payload["exit_code"].(float64); ok {
				rc = int(v)
			}
			payload, _ := json.Marshal(map[string]interface{}{"exit_code": rc})
			_, _ = fmt.Fprintln(conn, string(payload))
		case arcp.TypeJobCompleted, arcp.TypeJobFailed, arcp.TypeJobCancelled:
			return nil
		case arcp.TypeToolError:
			code, _ := env.Payload["code"].(string)
			msg, _ := env.Payload["message"].(string)
			return fmt.Errorf("%s: %s", code, msg)
		case arcp.TypeNack:
			code, _ := env.Payload["code"].(string)
			msg, _ := env.Payload["message"].(string)
			return fmt.Errorf("nack %s: %s", code, msg)
		}
		_ = ctx
	}
}

func (d *daemon) session(profileName string) (*daemonSession, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if s := d.sessions[profileName]; s != nil {
		return s, nil
	}
	p, err := profile.Load(profileName)
	if err != nil {
		return nil, err
	}
	conn, sid, err := dialAndOpen(p, 10*time.Second)
	if err != nil {
		return nil, err
	}
	s := &daemonSession{conn: conn, psk: p.PSK, sessionID: sid, openedAt: time.Now()}
	d.sessions[profileName] = s
	return s, nil
}

func (d *daemon) dropSession(profileName string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if s := d.sessions[profileName]; s != nil {
		_ = s.conn.Close()
		delete(d.sessions, profileName)
	}
}

func readPID(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0
	}
	return n
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// signal 0 = check existence
	return syscall.Kill(pid, 0) == nil
}
