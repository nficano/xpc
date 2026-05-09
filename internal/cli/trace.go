package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// xpc trace start|stop|pull
//
// Wraps Sysinternals procmon.exe on the VM. The agent stays out of this
// loop -- procmon is invoked directly via the agent's exec/python shell.
//
// Typical workflow:
//   xpc trace start --output C:\xpc\traces\foo.pml --filter '*notepad*'
//   ... do stuff ...
//   xpc trace stop
//   xpc trace pull C:\xpc\traces\foo.pml ./foo.pml

const defaultProcmonBinary = `C:\xpc\tools\procmon.exe`

func newTraceCmd(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trace",
		Short: "Sysinternals Process Monitor lifecycle on the VM (start/stop/pull).",
	}
	cmd.AddCommand(newTraceStartCmd(g))
	cmd.AddCommand(newTraceStopCmd(g))
	cmd.AddCommand(newTracePullCmd(g))
	return cmd
}

func newTraceStartCmd(g *Globals) *cobra.Command {
	var (
		binary, output string
		runtime        int
	)
	c := &cobra.Command{
		Use:   "start",
		Short: "Start procmon on the VM with a backing file (detached).",
		Long: `Launches procmon.exe /accepteula /quiet /minimized
/backingfile <output> [/runtime <seconds>] in the background. Use
` + "`xpc trace stop`" + ` to terminate manually, or supply --runtime to have
procmon stop itself.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if g.DryRun {
				cmd.Printf("(dry-run) %s /accepteula /quiet /minimized /backingfile %s%s\n",
					binary, output, runtimeSuffix(runtime))
				return nil
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			argv := []string{binary, "/accepteula", "/quiet", "/minimized", "/backingfile", output}
			if runtime > 0 {
				argv = append(argv, "/runtime", fmt.Sprintf("%d", runtime))
			}
			py := buildDetachedSpawnPy(argv, `C:\xpc\trace.runlog`)
			stdout, stderr, rc, err := runRemoteCmd(ctx, g, py, "python")
			if err != nil {
				return err
			}
			if rc != 0 {
				return &RemoteError{
					error:    fmt.Errorf("trace start rc=%d: %s", rc, strings.TrimSpace(stderr)),
					ExitCode: rc,
				}
			}
			cmd.Print(stdout)
			cmd.Printf("procmon started; backing file: %s\n", output)
			cmd.Println("Stop with `xpc trace stop`, then pull with `xpc trace pull`.")
			return nil
		},
	}
	c.Flags().StringVar(&binary, "binary", defaultProcmonBinary, "Path to procmon.exe on the VM")
	c.Flags().StringVar(&output, "output", `C:\xpc\traces\trace.pml`, "Backing-file path on the VM")
	c.Flags().IntVar(&runtime, "runtime", 0, "Auto-stop after N seconds (0 = manual stop)")
	return c
}

func newTraceStopCmd(g *Globals) *cobra.Command {
	var binary string
	c := &cobra.Command{
		Use:   "stop",
		Short: "Terminate the running procmon (procmon.exe /Terminate).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if g.DryRun {
				cmd.Printf("(dry-run) %s /Terminate\n", binary)
				return nil
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			argv := []string{binary, "/Terminate"}
			py := buildSubprocessPy(argv)
			stdout, stderr, rc, err := runRemoteCmd(ctx, g, py, "python")
			if err != nil {
				return err
			}
			cmd.Print(stdout)
			if rc != 0 {
				cmd.PrintErr(stderr)
				return &RemoteError{
					error:    fmt.Errorf("trace stop rc=%d", rc),
					ExitCode: rc,
				}
			}
			return nil
		},
	}
	c.Flags().StringVar(&binary, "binary", defaultProcmonBinary, "Path to procmon.exe on the VM")
	return c
}

func newTracePullCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "pull <vm-path> <host-path>",
		Short: "Copy a procmon backing file (.pml) back to the host. Alias for `xpc cp`.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			vmPath := stripVMPrefix(args[0])
			hostPath := args[1]
			return cpDownload(ctx, cmd, g, vmPath, hostPath)
		},
	}
}

func runtimeSuffix(seconds int) string {
	if seconds <= 0 {
		return ""
	}
	return fmt.Sprintf(" /runtime %d", seconds)
}
