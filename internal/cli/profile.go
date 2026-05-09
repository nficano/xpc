package cli

import (
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nficano/xpc/internal/profile"
)

func newProfileCmd(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Manage saved connection profiles (~/.xpc/config + ~/.xpc/credentials).",
	}
	cmd.AddCommand(newProfileListCmd(g))
	cmd.AddCommand(newProfileAddCmd(g))
	cmd.AddCommand(newProfileRemoveCmd(g))
	cmd.AddCommand(newProfileUseCmd(g))
	return cmd
}

func newProfileListCmd(_ *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List saved profile names. Active profile is starred.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			names, err := profile.List()
			if err != nil {
				return err
			}
			active, _ := profile.Active()
			if len(names) == 0 {
				cmd.Println("(no profiles)")
				return nil
			}
			for _, n := range names {
				marker := " "
				if n == active {
					marker = "*"
				}
				cmd.Println(marker, n)
			}
			return nil
		},
	}
}

func newProfileAddCmd(_ *Globals) *cobra.Command {
	var (
		host, fingerprint, sshUser, sshPassword string
		pskHex, pskFile                         string
		port                                    int
		verifyHost                              bool
	)
	c := &cobra.Command{
		Use:   "add <name>",
		Short: "Create a new profile non-interactively (use `xpc configure` for the prompted flow).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if strings.TrimSpace(name) == "" {
				return wrapUsage(fmt.Errorf("profile name must not be empty"))
			}

			var psk []byte
			if pskHex != "" {
				raw, err := hex.DecodeString(strings.TrimSpace(pskHex))
				if err != nil {
					return wrapUsage(fmt.Errorf("--psk-hex: %w", err))
				}
				psk = raw
			} else if pskFile != "" {
				raw, err := os.ReadFile(pskFile)
				if err != nil {
					return wrapUsage(fmt.Errorf("--psk-file: %w", err))
				}
				decoded, err := hex.DecodeString(strings.TrimSpace(string(raw)))
				if err != nil {
					return wrapUsage(fmt.Errorf("--psk-file: %w", err))
				}
				psk = decoded
			}
			if psk != nil && len(psk) != 32 {
				return wrapUsage(fmt.Errorf("PSK must decode to 32 bytes; got %d", len(psk)))
			}

			p := &profile.Profile{
				Name:          name,
				Host:          host,
				Port:          port,
				Fingerprint:   fingerprint,
				SSHUser:       sshUser,
				SSHPassword:   sshPassword,
				PSK:           psk,
				VerifyHostKey: verifyHost,
			}
			if err := profile.Save(p); err != nil {
				return err
			}
			cmd.Printf("saved profile %q\n", name)
			return nil
		},
	}
	c.Flags().StringVar(&host, "host", "", "VM hostname or IP")
	c.Flags().IntVar(&port, "port", 9578, "Agent TCP port")
	c.Flags().StringVar(&fingerprint, "fingerprint", "", "Pinned SHA-256 cert fingerprint")
	c.Flags().StringVar(&sshUser, "ssh-user", "", "SSH user (for bootstrap and agent lifecycle)")
	c.Flags().StringVar(&sshPassword, "ssh-password", "", "SSH password (stored in ~/.xpc/credentials)")
	c.Flags().StringVar(&pskHex, "psk-hex", "", "PSK as a 64-character hex string")
	c.Flags().StringVar(&pskFile, "psk-file", "", "Path to a hex-encoded PSK file (alternative to --psk-hex)")
	c.Flags().BoolVar(&verifyHost, "verify-host-key", true, "Verify SSH host key")
	return c
}

func newProfileRemoveCmd(_ *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Delete a saved profile.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := profile.Delete(args[0]); err != nil {
				return err
			}
			cmd.Printf("removed profile %q\n", args[0])
			return nil
		},
	}
}

func newProfileUseCmd(_ *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "use <name>",
		Short: "Set the active profile (writes ~/.xpc/state).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := profile.SetActive(args[0]); err != nil {
				return err
			}
			cmd.Printf("active profile -> %q\n", args[0])
			return nil
		},
	}
}

// xpc use <name> is the AWS-CLI-style top-level alias for `xpc profile use`.
func newUseCmd(_ *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "use <name>",
		Short: "Alias for `xpc profile use <name>`.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := profile.SetActive(args[0]); err != nil {
				return err
			}
			cmd.Printf("active profile -> %q\n", args[0])
			return nil
		},
	}
}
