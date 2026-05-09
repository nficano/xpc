package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newSvcCmd(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "svc",
		Short: "Windows services management (sc.exe wrapper).",
	}
	cmd.AddCommand(newSvcSubCmd(g, "list", "List all running services.", "net start", false))
	cmd.AddCommand(newSvcStateCmd(g, "start", "Start a service.", "sc start"))
	cmd.AddCommand(newSvcStateCmd(g, "stop", "Stop a service.", "sc stop"))
	cmd.AddCommand(newSvcStateCmd(g, "status", "Query service status.", "sc query"))
	cmd.AddCommand(newSvcInstallCmd(g))
	cmd.AddCommand(newSvcUninstallCmd(g))
	return cmd
}

func newSvcInstallCmd(g *Globals) *cobra.Command {
	var (
		displayName string
		startType   string
		account     string
		password    string
		depends     []string
	)
	c := &cobra.Command{
		Use:   "install <service> <binPath>",
		Short: "Install a Windows service via `sc create`.",
		Long: `Wraps the Windows ` + "`sc create`" + ` command. The binPath argument is the
full path to the service executable on the VM (e.g. ` + "`C:\\xpc\\daemon.exe`" + `).
Use --start to choose start type (auto, demand, disabled, boot, system).`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			service := args[0]
			binPath := args[1]
			argv := []string{"sc", "create", service, "binPath=", binPath}
			if startType != "" {
				argv = append(argv, "start=", startType)
			}
			if displayName != "" {
				argv = append(argv, "DisplayName=", displayName)
			}
			if account != "" {
				argv = append(argv, "obj=", account)
				if password != "" {
					argv = append(argv, "password=", password)
				}
			}
			if len(depends) > 0 {
				argv = append(argv, "depend=", strings.Join(depends, "/"))
			}
			if g.DryRun {
				cmd.Printf("(dry-run) %s\n", strings.Join(argv, " "))
				return nil
			}
			return runScPassthrough(cmd, g, argv, "sc create")
		},
	}
	c.Flags().StringVar(&displayName, "display-name", "", "Friendly display name shown in services.msc")
	c.Flags().StringVar(&startType, "start", "demand", "Start type: boot | system | auto | demand | disabled")
	c.Flags().StringVar(&account, "account", "", `Account to run as (default LocalSystem); e.g. "NT AUTHORITY\NetworkService"`)
	c.Flags().StringVar(&password, "password", "", "Password for --account (omit for LocalSystem and built-in accounts)")
	c.Flags().StringSliceVar(&depends, "depends", nil, "Service dependencies (repeatable or comma-separated)")
	return c
}

func newSvcUninstallCmd(g *Globals) *cobra.Command {
	var stopFirst bool
	c := &cobra.Command{
		Use:   "uninstall <service>",
		Short: "Delete a Windows service via `sc delete`.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			service := args[0]
			if g.DryRun {
				if stopFirst {
					cmd.Printf("(dry-run) sc stop %s\n", service)
				}
				cmd.Printf("(dry-run) sc delete %s\n", service)
				return nil
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			if stopFirst {
				stopArgv := []string{"sc", "stop", service}
				py := buildSubprocessPy(stopArgv)
				_, stderr, rc, err := runRemoteCmd(ctx, g, py, "python")
				if err != nil {
					return err
				}
				if rc != 0 && !isIdempotentSvcState(stderr) {
					cmd.PrintErrf("warning: sc stop %s exited rc=%d (continuing): %s\n", service, rc, strings.TrimSpace(stderr))
				}
			}
			return runScPassthrough(cmd, g, []string{"sc", "delete", service}, "sc delete")
		},
	}
	c.Flags().BoolVar(&stopFirst, "stop", true, "Stop the service before deleting (idempotent if already stopped)")
	return c
}

// runScPassthrough runs an sc.exe argv via the python subprocess wrapper
// (same approach as reg.go) so spaces in DisplayName / depend lists survive.
func runScPassthrough(cmd *cobra.Command, g *Globals, argv []string, what string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	py := buildSubprocessPy(argv)
	stdout, stderr, rc, err := runRemoteCmd(ctx, g, py, "python")
	if err != nil {
		return err
	}
	if err := requireSuccess(stdout, stderr, rc, what); err != nil {
		return err
	}
	cmd.Print(strings.TrimRight(stdout, "\r\n") + "\n")
	return nil
}

func newSvcSubCmd(g *Globals, name, short, remoteCmd string, _ bool) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			stdout, stderr, rc, err := runRemoteCmd(ctx, g, remoteCmd, "cmd")
			if err != nil {
				return err
			}
			if err := requireSuccess(stdout, stderr, rc, remoteCmd); err != nil {
				return err
			}
			cmd.Print(strings.TrimRight(stdout, "\r\n") + "\n")
			return nil
		},
	}
}

func newSvcStateCmd(g *Globals, name, short, prefix string) *cobra.Command {
	return &cobra.Command{
		Use:   name + " <service>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if g.DryRun {
				cmd.Printf("(dry-run) %s %s\n", prefix, args[0])
				return nil
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			remoteCmd := fmt.Sprintf("%s %q", prefix, args[0])
			stdout, stderr, rc, err := runRemoteCmd(ctx, g, remoteCmd, "cmd")
			if err != nil {
				return err
			}
			cmd.Print(strings.TrimRight(stdout, "\r\n") + "\n")
			// Idempotency: net/sc treat already-running/stopped as non-fatal
			// in xpc; warn but don't fail.
			if rc != 0 {
				if isIdempotentSvcState(stderr + stdout) {
					return nil
				}
				return &RemoteError{
					error:    fmt.Errorf("%s failed (rc=%d)", remoteCmd, rc),
					ExitCode: rc,
				}
			}
			return nil
		},
	}
}

func isIdempotentSvcState(s string) bool {
	lc := strings.ToLower(s)
	for _, marker := range []string{
		"is already running", "has not been started", "already been started",
	} {
		if strings.Contains(lc, marker) {
			return true
		}
	}
	return false
}
