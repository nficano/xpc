package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// xpc ghidra start|stop
//
// Thin wrappers that launch ghidra_server (or stop it) on the VM. The actual
// tunnel is opened separately with `xpc tun -L <local>:127.0.0.1:<port>` --
// kept decoupled so users can choose any local port and re-tunnel without
// restarting the server.
//
// Default --binary path follows the canonical Ghidra install layout. Users
// who put it elsewhere supply --binary.

func newGhidraCmd(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ghidra",
		Short: "Manage ghidra_server lifecycle on the VM (use `xpc tun` for the local tunnel).",
	}
	cmd.AddCommand(newGhidraStartCmd(g))
	cmd.AddCommand(newGhidraStopCmd(g))
	return cmd
}

func newGhidraStartCmd(g *Globals) *cobra.Command {
	var (
		binary string
		port   int
		repo   string
	)
	c := &cobra.Command{
		Use:   "start",
		Short: "Start ghidraSvr on the VM (detached).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if g.DryRun {
				cmd.Printf("(dry-run) start %s with port %d repo %q\n", binary, port, repo)
				return nil
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			args2 := []string{binary, fmt.Sprintf("-p%d", port)}
			if repo != "" {
				args2 = append(args2, "-r", repo)
			}
			py := buildDetachedSpawnPy(args2, `C:\xpc\ghidra.runlog`)
			stdout, stderr, rc, err := runRemoteCmd(ctx, g, py, "python")
			if err != nil {
				return err
			}
			if rc != 0 {
				return &RemoteError{
					error:    fmt.Errorf("ghidra start rc=%d: %s", rc, strings.TrimSpace(stderr)),
					ExitCode: rc,
				}
			}
			cmd.Print(stdout)
			cmd.Printf("ghidra_server started; expose with:\n  xpc tun -L %d:127.0.0.1:%d\n", port, port)
			return nil
		},
	}
	c.Flags().StringVar(&binary, "binary", `C:\ghidra\support\ghidraSvr.bat`, "Path to ghidraSvr.bat on the VM")
	c.Flags().IntVar(&port, "port", 13100, "ghidra_server listen port")
	c.Flags().StringVar(&repo, "repo", "", "Optional repository directory (-r)")
	return c
}

func newGhidraStopCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Kill any running ghidra_server (java.exe matching ghidra) on the VM.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if g.DryRun {
				cmd.Println("(dry-run) wmic kill java.exe matching ghidra")
				return nil
			}
			py := killByCmdLineMatch("java.exe", "ghidra")
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			stdout, stderr, rc, err := runRemoteCmd(ctx, g, py, "python")
			if err != nil {
				return err
			}
			if rc != 0 {
				return &RemoteError{
					error:    fmt.Errorf("ghidra stop rc=%d: %s", rc, strings.TrimSpace(stderr)),
					ExitCode: rc,
				}
			}
			cmd.Print(stdout)
			return nil
		},
	}
}

// buildDetachedSpawnPy emits a python source that subprocess.Popen-spawns
// argv with DETACHED_PROCESS, captures stdout/stderr to logFile, and exits
// immediately. Mirrors xpc bootstrap's manage.py detachment trick.
func buildDetachedSpawnPy(argv []string, logFile string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = pythonRepr(a)
	}
	return fmt.Sprintf(
		"import os, subprocess\n"+
			"DETACHED_PROCESS = 0x00000008\n"+
			"CREATE_NEW_PROCESS_GROUP = 0x00000200\n"+
			"# Detach stdio so the parent (this exec invocation) can return.\n"+
			"nul_r = os.open(os.devnull, os.O_RDONLY)\n"+
			"nul_w = os.open(os.devnull, os.O_WRONLY)\n"+
			"os.dup2(nul_r, 0); os.dup2(nul_w, 1); os.dup2(nul_w, 2)\n"+
			"os.close(nul_r); os.close(nul_w)\n"+
			"log = open(%[2]s, 'wb')\n"+
			"p = subprocess.Popen([%[1]s],\n"+
			"    stdin=subprocess.DEVNULL, stdout=log, stderr=subprocess.STDOUT,\n"+
			"    creationflags=DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP)\n"+
			"open(%[2]s + '.pid', 'w').write(str(p.pid))\n",
		strings.Join(parts, ", "), pythonRepr(logFile))
}

// killByCmdLineMatch emits python that wmics processes whose name matches
// imageName and whose command line contains needle, then taskkills each.
func killByCmdLineMatch(imageName, needle string) string {
	return fmt.Sprintf(
		"import subprocess\n"+
			"try:\n"+
			"    out = subprocess.check_output(\n"+
			"        ['wmic', 'process', 'where',\n"+
			"         \"name=%[1]s and commandline like %[2]s\",\n"+
			"         'get', 'processid', '/value'],\n"+
			"        stderr=subprocess.STDOUT,\n"+
			"    )\n"+
			"except subprocess.CalledProcessError as exc:\n"+
			"    out = exc.output or b''\n"+
			"killed = 0\n"+
			"for line in out.decode('utf-8','replace').splitlines():\n"+
			"    line = line.strip()\n"+
			"    if line.startswith('ProcessId='):\n"+
			"        pid = line.split('=', 1)[1].strip()\n"+
			"        if pid.isdigit() and int(pid) > 0:\n"+
			"            print('killing', pid)\n"+
			"            subprocess.call(['taskkill', '/F', '/PID', pid])\n"+
			"            killed += 1\n"+
			"print('killed', killed)\n",
		fmt.Sprintf("'%s'", imageName), fmt.Sprintf("'%%%s%%'", needle))
}
