package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nficano/xpc/internal/sshlife"
)

// xpc agent {start, stop, redeploy, tail}
//
// start    -- restart the agent via C:\xpc\manage.py restart on the profile's
//             port; assumes bootstrap has already deployed the files.
// stop     -- C:\xpc\manage.py kill (the manage helper kills only python.exe
//             matching C:\xpc\agent.py, not xpctl).
// redeploy -- alias for `xpc bootstrap` (recopies the agent + cert + PSK
//             from the bootstrap material dir, then restarts).
// tail     -- prints C:\xpc\agent.runlog over SSH for live debugging.

func newAgentStartCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the xpc agent on the VM (or restart it if it's already running).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return sshLifecycleAction(cmd, g, "restart")
		},
	}
}

func newAgentStopCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the xpc agent on the VM.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return sshLifecycleAction(cmd, g, "kill")
		},
	}
}

func newAgentRedeployCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "redeploy",
		Short: "Re-run xpc bootstrap (regenerate trust material + redeploy).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.Println("redeploy is `xpc bootstrap` -- run that with the desired --profile.")
			return nil
		},
	}
}

func newAgentTailCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "tail",
		Short: "Print C:\\xpc\\agent.runlog over SSH.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ssh, err := dialSSHFromProfile(g, 10*time.Second)
			if err != nil {
				return err
			}
			defer func() { _ = ssh.Close() }()
			stdout, stderr, rc, err := ssh.Run("cat /cygdrive/c/xpc/agent.runlog", 15*time.Second)
			if err != nil {
				return wrapConnection(err)
			}
			cmd.Print(stdout)
			if rc != 0 {
				cmd.PrintErr(stderr)
				return fmt.Errorf("cat agent.runlog rc=%d", rc)
			}
			return nil
		},
	}
}

func sshLifecycleAction(cmd *cobra.Command, g *Globals, action string) error {
	p, err := g.ResolveProfile()
	if err != nil {
		return err
	}
	if g.DryRun {
		cmd.Printf("(dry-run) ssh %s@%s -- C:\\Python34\\python.exe C:\\xpc\\manage.py %s %d\n",
			p.SSHUser, p.Host, action, p.Port)
		return nil
	}
	ssh, err := dialSSHFromProfile(g, 10*time.Second)
	if err != nil {
		return err
	}
	defer func() { _ = ssh.Close() }()

	// Win32 python.exe via Cygwin bash: POSIX path for the binary itself
	// (so bash can find it), but a single-quoted Win32 path for the script
	// argument because bash strips backslashes from unquoted args.
	remoteCmd := fmt.Sprintf(
		`/cygdrive/c/Python34/python.exe 'C:\xpc\manage.py' %s %d`,
		action, p.Port)
	stdout, stderr, rc, err := ssh.Run(remoteCmd, 30*time.Second)
	if err != nil {
		return wrapConnection(err)
	}
	if stdout != "" {
		cmd.Print(stdout)
	}
	if rc != 0 {
		return fmt.Errorf("manage.py %s rc=%d: %s",
			action, rc, strings.TrimSpace(stderr))
	}

	// For start/restart, wait until the agent is actually accepting TCP so
	// callers can chain `agent start; agent ping` without sleeping.
	if action == "start" || action == "restart" {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		if err := waitForListen(ctx, p.Host, p.Port, 15*time.Second); err != nil {
			return wrapConnection(err)
		}
	}
	cmd.Printf("agent %sed on %s:%d\n", actionVerb(action), p.Host, p.Port)
	return nil
}

func actionVerb(a string) string {
	switch a {
	case "kill":
		return "stopp"
	case "restart":
		return "restart"
	case "start":
		return "start"
	}
	return a
}

func dialSSHFromProfile(g *Globals, timeout time.Duration) (*sshlife.Client, error) {
	p, err := g.ResolveProfile()
	if err != nil {
		return nil, err
	}
	if p.Host == "" {
		return nil, wrapUsage(fmt.Errorf("profile %q has no host", p.Name))
	}
	if p.SSHUser == "" || p.SSHPassword == "" {
		return nil, wrapUsage(fmt.Errorf(
			"profile %q is missing ssh_user/ssh_password (run `xpc configure --profile %s`)",
			p.Name, p.Name))
	}
	c, err := sshlife.Dial(p.Host+":22", sshlife.DialOptions{
		User: p.SSHUser, Password: p.SSHPassword, Timeout: timeout,
	})
	if err != nil {
		return nil, wrapConnection(err)
	}
	return c, nil
}
