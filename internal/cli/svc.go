package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newSvcCmd(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "svc",
		Short: "Windows services management (sc.exe wrapper).",
	}
	cmd.AddCommand(newSvcSubCmd(g, "list", "List all running services.", "net start", false))
	cmd.AddCommand(newSvcStateCmd(g, "start", "Start a service.", "sc start"))
	cmd.AddCommand(newSvcStateCmd(g, "stop", "Stop a service.", "sc stop"))
	cmd.AddCommand(newSvcStateCmd(g, "status", "Query service status.", "sc query"))
	return cmd
}

func newSvcSubCmd(g *Globals, name, short, remoteCmd string, _ bool) *cobra.Command {
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

func newSvcStateCmd(g *Globals, name, short, prefix string) *cobra.Command {
	return &cobra.Command{
		Use:   name + " <service>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if g.DryRun {
				cmd.Printf("(dry-run) %s %s\n", prefix, args[0])
				return nil
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			remoteCmd := fmt.Sprintf("%s %q", prefix, args[0])
			stdout, stderr, rc, err := runRemoteCmd(ctx, g, remoteCmd, "cmd")
			if err != nil {
				return err
			}
			cmd.Print(strings.TrimRight(stdout, "\r\n") + "\n")
			// Idempotency: net/sc treat already-running/stopped as non-fatal
			// in xpc; warn but don't fail.
			if rc != 0 {
				if isIdempotentSvcState(stderr + stdout) {
					return nil
				}
				return &RemoteError{
					error:    fmt.Errorf("%s failed (rc=%d)", remoteCmd, rc),
					ExitCode: rc,
				}
			}
			return nil
		},
	}
}

func isIdempotentSvcState(s string) bool {
	lc := strings.ToLower(s)
	for _, marker := range []string{
		"is already running", "has not been started", "already been started",
	} {
		if strings.Contains(lc, marker) {
			return true
		}
	}
	return false
}
