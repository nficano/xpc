package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newBatCmd(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bat",
		Short: "Run .bat files on the VM.",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "run <vm:path> [args...]",
		Short: "Invoke a .bat file already on the VM.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := stripVMPrefix(args[0])
			parts := append([]string{fmt.Sprintf("%q", path)}, args[1:]...)
			line := strings.Join(parts, " ")
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			stdout, stderr, rc, err := runRemoteCmd(ctx, g, line, "cmd")
			if err != nil {
				return err
			}
			cmd.Print(stdout)
			cmd.PrintErr(stderr)
			if rc != 0 {
				return &RemoteError{
					error:    fmt.Errorf("%s exited %d", path, rc),
					ExitCode: rc,
				}
			}
			return nil
		},
	})
	return cmd
}
