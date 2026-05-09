package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newNetCmd(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "net",
		Short: "Network diagnostics: ipconfig, netstat, route.",
		Long: `By default ('xpc net' with no subcommand) prints a combined
ipconfig /all, netstat -ano, and route print summary -- the same view xpctl's
'net' subcommand provided.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			out := cmd.OutOrStdout()
			for _, sec := range []struct {
				heading, cmd string
			}{
				{"=== ipconfig /all ===", "ipconfig /all"},
				{"=== netstat -ano ===", "netstat -ano"},
				{"=== route print ===", "route print"},
			} {
				stdout, stderr, rc, err := runRemoteCmd(ctx, g, sec.cmd, "cmd")
				if err != nil {
					return err
				}
				if err := requireSuccess(stdout, stderr, rc, sec.cmd); err != nil {
					return err
				}
				fmt.Fprintln(out)
				fmt.Fprintln(out, sec.heading)
				fmt.Fprintln(out, strings.TrimRight(stdout, "\r\n"))
			}
			return nil
		},
	}
	cmd.AddCommand(newNetSubCmd(g, "ipconfig", "Show ipconfig /all.", "ipconfig /all"))
	cmd.AddCommand(newNetSubCmd(g, "netstat", "Show netstat -ano.", "netstat -ano"))
	cmd.AddCommand(newNetSubCmd(g, "route", "Show the routing table.", "route print"))
	return cmd
}

func newNetSubCmd(g *Globals, name, short, remoteCmd string) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			stdout, stderr, rc, err := runRemoteCmd(ctx, g, remoteCmd, "cmd")
			if err != nil {
				return err
			}
			if err := requireSuccess(stdout, stderr, rc, remoteCmd); err != nil {
				return err
			}
			cmd.Print(strings.TrimRight(stdout, "\r\n") + "\n")
			return nil
		},
	}
}
