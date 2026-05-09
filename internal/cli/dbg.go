package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// xpc dbg run|analyze
//
// One-shot debugger wrappers. cdb is the canonical pick because it captures
// stdout cleanly. For interactive sessions, use the live debuggers directly
// over an `xpc tun -L`-exposed dbgsrv (see `xpc ida start`).
//
// run     -- launch a target under cdb and run user-supplied commands.
// analyze -- shorthand for `dbg run --command "!analyze -v" <dump>` against
//            a minidump file.

const defaultCdbBinary = `C:\Program Files\Debugging Tools for Windows\cdb.exe`

func newDbgCmd(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dbg",
		Short: "Debugger wrappers (one-shot cdb invocations).",
	}
	cmd.AddCommand(newDbgRunCmd(g))
	cmd.AddCommand(newDbgAnalyzeCmd(g))
	return cmd
}

func newDbgRunCmd(g *Globals) *cobra.Command {
	var (
		binary, command string
	)
	c := &cobra.Command{
		Use:   "run <target>",
		Short: "Launch <target> under cdb, run --command, capture output, exit.",
		Long: `<target> is either a path on the VM (e.g. C:\path\to\app.exe) or a
crash-dump file (.dmp). cdb runs --command then quits via the appended ;q.
Pass commands separated by ;`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if g.DryRun {
				cmd.Printf("(dry-run) %s -c %q -z|-cf %s\n", binary, command+";q", args[0])
				return nil
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			full := strings.TrimSpace(command)
			if full == "" {
				full = "lm" // default: list modules
			}
			full += ";q"
			target := args[0]
			argv := []string{binary, "-c", full}
			if strings.HasSuffix(strings.ToLower(target), ".dmp") {
				argv = append(argv, "-z", target)
			} else {
				argv = append(argv, target)
			}
			py := buildSubprocessPy(argv)
			stdout, stderr, rc, err := runRemoteCmd(ctx, g, py, "python")
			if err != nil {
				return err
			}
			cmd.Print(stdout)
			if stderr != "" {
				cmd.PrintErr(stderr)
			}
			if rc != 0 {
				return &RemoteError{
					error:    fmt.Errorf("cdb rc=%d", rc),
					ExitCode: rc,
				}
			}
			return nil
		},
	}
	c.Flags().StringVar(&binary, "binary", defaultCdbBinary, "Path to cdb.exe on the VM")
	c.Flags().StringVarP(&command, "command", "c", "lm", "cdb command(s) to run; ;q is auto-appended")
	return c
}

func newDbgAnalyzeCmd(g *Globals) *cobra.Command {
	var binary string
	c := &cobra.Command{
		Use:   "analyze <vm:dump.dmp>",
		Short: "Run cdb -c '!analyze -v' against a minidump on the VM.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if g.DryRun {
				cmd.Printf("(dry-run) %s -c '!analyze -v;q' -z %s\n", binary, args[0])
				return nil
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			argv := []string{binary, "-c", "!analyze -v;q", "-z", stripVMPrefix(args[0])}
			py := buildSubprocessPy(argv)
			stdout, _, rc, err := runRemoteCmd(ctx, g, py, "python")
			if err != nil {
				return err
			}
			cmd.Print(stdout)
			if rc != 0 {
				return &RemoteError{
					error:    fmt.Errorf("cdb rc=%d", rc),
					ExitCode: rc,
				}
			}
			return nil
		},
	}
	c.Flags().StringVar(&binary, "binary", defaultCdbBinary, "Path to cdb.exe on the VM")
	return c
}
