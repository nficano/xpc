package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newDllCmd(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dll",
		Short: "DLL helpers (list loaded modules, regsvr32).",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list <pid>",
		Short: "List DLL modules loaded by a process (tasklist /m /fi \"PID eq <pid>\").",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			line := fmt.Sprintf(`tasklist /m /fi "PID eq %s"`, args[0])
			stdout, stderr, rc, err := runRemoteCmd(ctx, g, line, "cmd")
			if err != nil {
				return err
			}
			if err := requireSuccess(stdout, stderr, rc, "tasklist /m"); err != nil {
				return err
			}
			cmd.Print(strings.TrimRight(stdout, "\r\n") + "\n")
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "regsvr32 <vm:path-to-dll>",
		Short: "Run regsvr32 /s on the VM. --unregister to flip to /u.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			unreg, _ := cmd.Flags().GetBool("unregister")
			line := fmt.Sprintf("regsvr32 /s %s%s",
				ifElse(unreg, "/u ", ""), quoteRegArg(stripVMPrefix(args[0])))
			if g.DryRun {
				cmd.Printf("(dry-run) %s\n", line)
				return nil
			}
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
				return &RemoteError{error: fmt.Errorf("regsvr32 rc=%d", rc), ExitCode: rc}
			}
			return nil
		},
	})
	cmd.PersistentFlags().Bool("unregister", false, "Pass /u to regsvr32 (unregister)")
	return cmd
}

func ifElse(b bool, a, c string) string {
	if b {
		return a
	}
	return c
}
