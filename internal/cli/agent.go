package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nficano/xpc/internal/arcp"
	"github.com/nficano/xpc/internal/output"
)

func newAgentCmd(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Lifecycle and diagnostics for the on-VM xpc agent.",
	}
	cmd.AddCommand(newAgentPingCmd(g))
	cmd.AddCommand(newAgentInfoCmd(g))
	cmd.AddCommand(newAgentStartCmd(g))
	cmd.AddCommand(newAgentStopCmd(g))
	cmd.AddCommand(newAgentRedeployCmd(g))
	cmd.AddCommand(newAgentTailCmd(g))
	return cmd
}

func newAgentPingCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "ping",
		Short: "Open a session and exchange ping/pong with the agent.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := g.ResolveProfile()
			if err != nil {
				return err
			}
			conn, sid, err := dialAndOpen(p, g.Timeout)
			if err != nil {
				return err
			}
			defer func() {
				closeSession(conn, p.PSK, sid)
				_ = conn.Close()
			}()

			ping := arcp.New(arcp.MustNewID(arcp.PrefixMessage), arcp.TypePing, arcp.FormatTimestamp(time.Now()))
			ping.SessionID = sid
			ping.Payload = map[string]any{}
			if err := arcp.Sign(ping, p.PSK); err != nil {
				return err
			}
			start := time.Now()
			if err := arcp.WriteFrame(conn, ping); err != nil {
				return wrapConnection(err)
			}
			pong, err := readSignedFrame(conn, p.PSK, 5*time.Second)
			if err != nil {
				return err
			}
			elapsed := time.Since(start)
			if pong.Type != arcp.TypePong {
				return fmt.Errorf("expected pong; got %s", pong.Type)
			}
			cmd.Printf("pong from %s in %s\n", p.Host, elapsed)
			return nil
		},
	}
}

func newAgentInfoCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Invoke the agent.info tool and print its result.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := g.ResolveProfile()
			if err != nil {
				return err
			}
			conn, sid, err := dialAndOpen(p, g.Timeout)
			if err != nil {
				return err
			}
			defer func() {
				closeSession(conn, p.PSK, sid)
				_ = conn.Close()
			}()

			invoke := arcp.New(arcp.MustNewID(arcp.PrefixMessage), arcp.TypeToolInvoke, arcp.FormatTimestamp(time.Now()))
			invoke.SessionID = sid
			invoke.Payload = map[string]any{
				"tool":      "agent.info",
				"arguments": map[string]any{},
			}
			if err := arcp.Sign(invoke, p.PSK); err != nil {
				return err
			}
			if err := arcp.WriteFrame(conn, invoke); err != nil {
				return wrapConnection(err)
			}

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			var info map[string]any
			for {
				env, err := readSignedFrame(conn, p.PSK, 5*time.Second)
				if err != nil {
					return err
				}
				switch env.Type {
				case arcp.TypeJobAccepted, arcp.TypeJobStarted:
					continue
				case arcp.TypeToolResult:
					info = env.Payload
				case arcp.TypeJobCompleted:
					if info == nil {
						return fmt.Errorf("missing tool.result before job.completed")
					}
					return printInfo(cmd, info, g.OutputMode)
				case arcp.TypeJobFailed, arcp.TypeJobCancelled:
					return fmt.Errorf("job ended with %s", env.Type)
				case arcp.TypeToolError:
					code, _ := env.Payload["code"].(string)
					msg, _ := env.Payload["message"].(string)
					return fmt.Errorf("tool.error %s: %s", code, msg)
				case arcp.TypeNack:
					code, _ := env.Payload["code"].(string)
					msg, _ := env.Payload["message"].(string)
					return fmt.Errorf("nack %s: %s", code, msg)
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
			}
		},
	}
}

func printInfo(cmd *cobra.Command, info map[string]any, mode string) error {
	m := output.ParseMode(mode)
	if m == output.ModeJSON {
		return output.Encode(cmd.OutOrStdout(), m, info)
	}
	pairs := make([]output.KV, 0, len(info)+3)
	if agent, ok := info["agent"].(map[string]any); ok {
		pairs = append(pairs, output.KV{Key: "agent", Value: fmt.Sprintf("%s v%s", agent["name"], agent["version"])})
		if py, ok := agent["python"].(string); ok && py != "" {
			pairs = append(pairs, output.KV{Key: "python", Value: py})
		}
		if pid, ok := agent["pid"].(float64); ok {
			pairs = append(pairs, output.KV{Key: "pid", Value: int(pid)})
		}
	}
	if uptime, ok := info["uptime_seconds"].(float64); ok {
		pairs = append(pairs, output.KV{Key: "uptime", Value: time.Duration(uptime * float64(time.Second)).Truncate(time.Second)})
	}
	for k, v := range info {
		if k == "agent" || k == "uptime_seconds" {
			continue
		}
		pairs = append(pairs, output.KV{Key: k, Value: strings.TrimSpace(fmt.Sprint(v))})
	}
	return output.EncodeKV(cmd.OutOrStdout(), m, pairs)
}
