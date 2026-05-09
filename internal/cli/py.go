package cli

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/nficano/xpc/internal/arcp"
)

func newPyCmd(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "py",
		Short: "Run Python on the VM (Python 3.4 / Windows XP).",
	}
	cmd.AddCommand(newPyRunCmd(g))
	cmd.AddCommand(newPyLocalCmd(g))
	cmd.AddCommand(newPyPipCmd(g))
	cmd.AddCommand(newPyReplCmd(g))
	return cmd
}

func newPyReplCmd(g *Globals) *cobra.Command {
	var pythonExe string
	c := &cobra.Command{
		Use:   "repl",
		Short: "Open an interactive Python REPL on the VM (line-buffered).",
		Long: `Spawns ` + "`python -i -u`" + ` on the VM and proxies stdin/stdout/stderr
through the agent. The session is line-buffered: each line you type is
sent to the interpreter on Enter; Ctrl-D (EOF) ends the session.

Caveats:
* Tab-completion and arrow-key history don't traverse the link in v0.
* Output is forwarded as the interpreter writes it; for prompt-driven
  interaction this means the ` + "`>>> `" + ` prompt arrives first, then your
  input is echoed back when the agent's terminal echoes it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runPyRepl(ctx, g, cmd, pythonExe)
		},
	}
	c.Flags().StringVar(&pythonExe, "python", "", `Override python.exe path on the VM (default C:\Python34\python.exe)`)
	return c
}

// runPyRepl drives the bidirectional REPL session against the agent's
// py.repl tool. Modeled on handleTunConn but with stdin/stdout/stderr
// instead of a TCP socket.
func runPyRepl(ctx context.Context, g *Globals, cmd *cobra.Command, pythonExe string) error {
	p, err := g.ResolveProfile()
	if err != nil {
		return err
	}
	conn, sessionID, err := dialAndOpen(p, g.Timeout)
	if err != nil {
		return err
	}
	defer func() {
		closeSession(conn, p.PSK, sessionID)
		_ = conn.Close()
	}()

	var writeMu sync.Mutex
	send := func(env *arcp.Envelope) error {
		if err := arcp.Sign(env, p.PSK); err != nil {
			return err
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		return arcp.WriteFrame(conn, env)
	}

	traceID := arcp.MustNewID(arcp.PrefixTrace)
	invoke := arcp.New(arcp.MustNewID(arcp.PrefixMessage), arcp.TypeToolInvoke, arcp.FormatTimestamp(time.Now()))
	invoke.SessionID = sessionID
	invoke.TraceID = traceID
	args := map[string]any{}
	if pythonExe != "" {
		args["python_exe"] = pythonExe
	}
	invoke.Payload = map[string]any{"tool": "py.repl", "arguments": args}
	if err := send(invoke); err != nil {
		return wrapConnection(err)
	}

	stdinStreamID := arcp.MustNewID(arcp.PrefixStream)
	jobReady := make(chan string, 1)
	closed := make(chan struct{})
	readerErr := make(chan error, 1)
	streamChannels := map[string]string{} // stream_id -> "stdout" | "stderr"
	var jobID string
	exitCode := -1

	go func() {
		defer close(closed)
		for {
			env, err := arcp.ReadFrame(conn)
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
				select {
				case jobReady <- env.JobID:
				default:
				}
			case arcp.TypeJobStarted:
				// informational
			case arcp.TypeStreamOpen:
				ch, _ := env.Payload["channel"].(string)
				streamChannels[env.StreamID] = ch
			case arcp.TypeStreamChunk:
				delta, _ := env.Payload["delta_b64"].(string)
				if delta == "" {
					continue
				}
				raw, dec := base64.StdEncoding.DecodeString(delta)
				if dec != nil {
					readerErr <- fmt.Errorf("decode stream delta_b64: %w", dec)
					return
				}
				w := cmd.OutOrStdout()
				if streamChannels[env.StreamID] == "stderr" {
					w = cmd.ErrOrStderr()
				}
				_, _ = w.Write(raw)
			case arcp.TypeStreamClose, arcp.TypeStreamError:
				delete(streamChannels, env.StreamID)
			case arcp.TypeToolResult:
				if v, ok := env.Payload["exit_code"].(float64); ok {
					exitCode = int(v)
				}
			case arcp.TypeJobCompleted, arcp.TypeJobFailed, arcp.TypeJobCancelled:
				return
			case arcp.TypeToolError:
				code, _ := env.Payload["code"].(string)
				msg, _ := env.Payload["message"].(string)
				readerErr <- &RemoteError{error: fmt.Errorf("py.repl error %s: %s", code, msg), ExitCode: 5}
				return
			case arcp.TypeNack:
				code, _ := env.Payload["code"].(string)
				msg, _ := env.Payload["message"].(string)
				readerErr <- fmt.Errorf("nack %s: %s", code, msg)
				return
			}
		}
	}()

	// Stdin pump: read lines from os.Stdin and forward as stream.chunks.
	stdinDone := make(chan struct{})
	go func() {
		defer close(stdinDone)
		var jid string
		select {
		case jid = <-jobReady:
		case <-closed:
			return
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Second):
			return
		}
		reader := bufio.NewReader(os.Stdin)
		buf := make([]byte, 4096)
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				chunk := arcp.New(arcp.MustNewID(arcp.PrefixMessage),
					arcp.TypeStreamChunk, arcp.FormatTimestamp(time.Now()))
				chunk.SessionID = sessionID
				chunk.JobID = jid
				chunk.StreamID = stdinStreamID
				chunk.TraceID = traceID
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
				closeEv.JobID = jid
				closeEv.StreamID = stdinStreamID
				closeEv.TraceID = traceID
				closeEv.Payload = map[string]any{"reason": "host_eof"}
				_ = send(closeEv)
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
	// Best-effort wait for stdin pump to clean up.
	select {
	case <-stdinDone:
	case <-time.After(100 * time.Millisecond):
	}
	if exitCode > 0 {
		return &RemoteError{error: fmt.Errorf("python exited %d", exitCode), ExitCode: exitCode}
	}
	return nil
}

func newPyRunCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "run -- <python source>",
		Short: "Execute a Python source string on the VM (python -c).",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			line := strings.Join(args, " ")
			stdout, stderr, rc, err := runRemoteCmd(ctx, g, line, "python")
			if err != nil {
				return err
			}
			cmd.Print(stdout)
			cmd.PrintErr(stderr)
			if rc != 0 {
				return &RemoteError{
					error:    fmt.Errorf("python exited %d", rc),
					ExitCode: rc,
				}
			}
			return nil
		},
	}
}

func newPyLocalCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "local <local.py>",
		Short: "Read a local .py file and run it on the VM as a single python -c invocation.",
		Long: `Reads <local.py> from the host filesystem and ships its contents to the
agent's exec tool with --shell python. This mirrors xpctl's "script"
subcommand: a one-shot script run, where the script source is local but
execution is on the VM. For larger workflows use xpc cp + xpc py run.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := os.ReadFile(args[0])
			if err != nil {
				return wrapUsage(fmt.Errorf("read %s: %w", args[0], err))
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			stdout, stderr, rc, err := runRemoteCmd(ctx, g, string(data), "python")
			if err != nil {
				return err
			}
			cmd.Print(stdout)
			cmd.PrintErr(stderr)
			if rc != 0 {
				return &RemoteError{
					error:    fmt.Errorf("%s exited %d", args[0], rc),
					ExitCode: rc,
				}
			}
			return nil
		},
	}
}

func newPyPipCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "pip [args...]",
		Short: "Forward args to the VM's `python -m pip`.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			line := "C:\\Python34\\python.exe -m pip " + strings.Join(args, " ")
			stdout, stderr, rc, err := runRemoteCmd(ctx, g, line, "cmd")
			if err != nil {
				return err
			}
			cmd.Print(stdout)
			cmd.PrintErr(stderr)
			if rc != 0 {
				return &RemoteError{
					error:    fmt.Errorf("pip exited %d", rc),
					ExitCode: rc,
				}
			}
			return nil
		},
	}
}
