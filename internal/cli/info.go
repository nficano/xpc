package cli

import (
	"context"
	"strings"

	"github.com/spf13/cobra"
)

func newInfoCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Print Windows systeminfo from the VM.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			stdout, stderr, rc, err := runRemoteCmd(ctx, g, "systeminfo", "cmd")
			if err != nil {
				return err
			}
			if err := requireSuccess(stdout, stderr, rc, "systeminfo"); err != nil {
				return err
			}
			cmd.Print(strings.TrimRight(stdout, "\r\n") + "\n")
			return nil
		},
	}
}
