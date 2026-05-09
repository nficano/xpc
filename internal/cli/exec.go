package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newExecCmd(g *Globals) *cobra.Command {
	var shell string
	c := &cobra.Command{
		Use:   "exec [--shell cmd|python|python_file] -- <command> [args...]",
		Short: "Run a command on the remote XP VM and stream its output.",
		Long: `Run <command> on the remote VM via the agent's exec tool.

The command is sent verbatim to the chosen shell:
  --shell cmd          (default) wrap with cmd.exe /c
  --shell python       run as Python source via -c
  --shell python_file  run a .py file already on the VM

stdout and stderr stream to the local terminal as they arrive on the VM. The
process exit code propagates as the xpc exit code.`,
		DisableFlagParsing: false,
		Args:               cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := g.ResolveProfile()
			if err != nil {
				return err
			}
			cmdLine := strings.Join(args, " ")
			conn, sid, err := dialAndOpen(p, g.Timeout)
			if err != nil {
				return err
			}
			defer func() {
				closeSession(conn, p.PSK, sid)
				_ = conn.Close()
			}()

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			rc, err := invokeExec(ctx, conn, p.PSK, sid, "",
				cmdLine, shell, int(g.Timeout.Seconds()),
				cmd.OutOrStdout(), cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if rc != 0 {
				return &RemoteError{error: fmt.Errorf("remote exit code %d", rc), ExitCode: rc}
			}
			return nil
		},
	}
	c.Flags().StringVar(&shell, "shell", "cmd", "Remote shell: cmd | python | python_file")
	return c
}
