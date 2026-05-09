package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newEnvCmd(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Environment variables on the VM.",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "Print 'set' output from the VM.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			stdout, stderr, rc, err := runRemoteCmd(ctx, g, "set", "cmd")
			if err != nil {
				return err
			}
			if err := requireSuccess(stdout, stderr, rc, "set"); err != nil {
				return err
			}
			cmd.Print(strings.TrimRight(stdout, "\r\n") + "\n")
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "set <name> <value>",
		Short: "Persistently set an environment variable on the VM (setx).",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if g.DryRun {
				cmd.Printf("(dry-run) setx %q %q\n", args[0], args[1])
				return nil
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			remoteCmd := fmt.Sprintf("setx %q %q", args[0], args[1])
			stdout, stderr, rc, err := runRemoteCmd(ctx, g, remoteCmd, "cmd")
			if err != nil {
				return err
			}
			cmd.Print(strings.TrimRight(stdout, "\r\n") + "\n")
			return requireSuccess(stdout, stderr, rc, "setx")
		},
	})
	return cmd
}
