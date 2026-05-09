package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nficano/xpc/internal/arcp"
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
			ping.Payload = map[string]interface{}{}
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
			invoke.Payload = map[string]interface{}{
				"tool":      "agent.info",
				"arguments": map[string]interface{}{},
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

			var info map[string]interface{}
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

func printInfo(cmd *cobra.Command, info map[string]interface{}, mode string) error {
	if mode == "json" {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(info)
	}
	if agent, ok := info["agent"].(map[string]interface{}); ok {
		cmd.Printf("agent:    %s v%s\n", agent["name"], agent["version"])
		if py, ok := agent["python"].(string); ok && py != "" {
			cmd.Printf("python:   %s\n", py)
		}
		if pid, ok := agent["pid"].(float64); ok {
			cmd.Printf("pid:      %d\n", int(pid))
		}
	}
	if uptime, ok := info["uptime_seconds"].(float64); ok {
		cmd.Printf("uptime:   %s\n", time.Duration(uptime*float64(time.Second)).Truncate(time.Second))
	}
	for k, v := range info {
		if k == "agent" || k == "uptime_seconds" {
			continue
		}
		cmd.Printf("%-9s %v\n", k+":", strings.TrimSpace(fmt.Sprint(v)))
	}
	return nil
}
