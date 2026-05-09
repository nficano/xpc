package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// xpc ida start|stop
//
// Wraps IDA's win32_remote.exe / dbgsrv on the VM, with the same lifecycle
// pattern as xpc ghidra. The local tunnel is `xpc tun -L`.

func newIdaCmd(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ida",
		Short: "Manage IDA's remote debug stub (dbgsrv / win32_remote) on the VM.",
	}
	cmd.AddCommand(newIdaStartCmd(g))
	cmd.AddCommand(newIdaStopCmd(g))
	return cmd
}

func newIdaStartCmd(g *Globals) *cobra.Command {
	var (
		binary string
		port   int
	)
	c := &cobra.Command{
		Use:   "start",
		Short: "Start IDA's remote debug stub on the VM (detached).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if g.DryRun {
				cmd.Printf("(dry-run) start %s --port %d\n", binary, port)
				return nil
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			argv := []string{binary, "-p", fmt.Sprintf("%d", port)}
			py := buildDetachedSpawnPy(argv, `C:\xpc\ida.runlog`)
			stdout, stderr, rc, err := runRemoteCmd(ctx, g, py, "python")
			if err != nil {
				return err
			}
			if rc != 0 {
				return &RemoteError{
					error:    fmt.Errorf("ida start rc=%d: %s", rc, strings.TrimSpace(stderr)),
					ExitCode: rc,
				}
			}
			cmd.Print(stdout)
			cmd.Printf("IDA debug stub started; expose with:\n  xpc tun -L %d:127.0.0.1:%d\n", port, port)
			return nil
		},
	}
	c.Flags().StringVar(&binary, "binary", `C:\IDA\dbgsrv\win32_remote.exe`, "Path to IDA's remote debug stub on the VM")
	c.Flags().IntVar(&port, "port", 23946, "Listen port (IDA default: 23946)")
	return c
}

func newIdaStopCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Kill any running IDA debug stub on the VM.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if g.DryRun {
				cmd.Println("(dry-run) wmic kill win32_remote.exe / dbgsrv.exe")
				return nil
			}
			py := killByCmdLineMatch("win32_remote.exe", "")
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			stdout, _, _, err := runRemoteCmd(ctx, g, py, "python")
			if err != nil {
				return err
			}
			cmd.Print(stdout)
			// Also try the other common name.
			py2 := killByCmdLineMatch("dbgsrv.exe", "")
			stdout2, _, _, _ := runRemoteCmd(ctx, g, py2, "python")
			cmd.Print(stdout2)
			return nil
		},
	}
}
