package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func newPyCmd(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "py",
		Short: "Run Python on the VM (Python 3.4 / Windows XP).",
	}
	cmd.AddCommand(newPyRunCmd(g))
	cmd.AddCommand(newPyLocalCmd(g))
	cmd.AddCommand(newPyPipCmd(g))
	return cmd
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
