package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

// xpc boot {reboot, shutdown}
//
// reboot/shutdown wrap the Win32 `shutdown.exe`. pause/resume require
// Proxmox API integration and are deferred until profile.proxmox_host /
// proxmox_user are wired up (see TASKS.md "Open questions for user").

func newBootCmd(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "boot",
		Short: "Boot lifecycle: reboot, shutdown, pause, resume.",
	}
	cmd.AddCommand(newBootShutdownLikeCmd(g, "reboot", "Reboot the VM (shutdown.exe /r /f).", "shutdown.exe /r /f /t 0"))
	cmd.AddCommand(newBootShutdownLikeCmd(g, "shutdown", "Power off the VM (shutdown.exe /s /f).", "shutdown.exe /s /f /t 0"))
	cmd.AddCommand(newBootPauseResumeStub(g, "pause", "Pause the VM via Proxmox (deferred)."))
	cmd.AddCommand(newBootPauseResumeStub(g, "resume", "Resume the VM via Proxmox (deferred)."))
	return cmd
}

func newBootShutdownLikeCmd(g *Globals, name, short, remoteCmd string) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if g.DryRun {
				cmd.Printf("(dry-run) %s\n", remoteCmd)
				return nil
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			// shutdown.exe forks a process and exits immediately; the agent's
			// exec wrapper returns success even though the box goes down a
			// second later.
			stdout, _, rc, err := runRemoteCmd(ctx, g, remoteCmd, "cmd")
			if err != nil {
				return err
			}
			cmd.Print(stdout)
			if rc != 0 {
				return &RemoteError{
					error:    fmt.Errorf("%s rc=%d", remoteCmd, rc),
					ExitCode: rc,
				}
			}
			cmd.Printf("%s issued; agent will drop in a moment\n", name)
			return nil
		},
	}
}

func newBootPauseResumeStub(_ *Globals, name, short string) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return wrapUsage(fmt.Errorf(
				"%s requires Proxmox host + auth in the profile (proxmox_host, proxmox_user). See TASKS.md open questions",
				name))
		},
	}
}
