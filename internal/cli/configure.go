package cli

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nficano/xpc/internal/profile"
)

func newConfigureCmd(g *Globals) *cobra.Command {
	var name string
	c := &cobra.Command{
		Use:   "configure",
		Short: "Interactively set up a profile in ~/.xpc/config + ~/.xpc/credentials.",
		Long: `Walks through host/port/SSH-user prompts and saves the result. Use
` + "`xpc bootstrap`" + ` afterward (or instead) to deploy the agent and pin a
fingerprint via TOFU.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			profileName := name
			if profileName == "" {
				profileName = g.ProfileName
			}
			if profileName == "" {
				active, _ := profile.Active()
				profileName = active
			}
			if profileName == "" {
				profileName = profile.DefaultName
			}

			existing, _ := profile.Load(profileName)
			r := bufio.NewReader(cmd.InOrStdin())
			out := cmd.OutOrStdout()

			fmt.Fprintf(out, "Configuring profile %q. Press Enter to accept the default in [brackets].\n\n", profileName)

			host := promptString(r, out, "Hostname or IP", existing.Host)
			port := promptInt(r, out, "Port", existing.Port, 9578)
			sshUser := promptString(r, out, "SSH user (for bootstrap)", existing.SSHUser)
			sshPassword := promptString(r, out, "SSH password (stored in ~/.xpc/credentials)", existing.SSHPassword)

			p := &profile.Profile{
				Name:          profileName,
				Host:          host,
				Port:          port,
				Fingerprint:   existing.Fingerprint,
				SSHUser:       sshUser,
				SSHPassword:   sshPassword,
				PSK:           existing.PSK,
				ProxmoxHost:   existing.ProxmoxHost,
				ProxmoxUser:   existing.ProxmoxUser,
				VerifyHostKey: true,
			}
			if err := profile.Save(p); err != nil {
				return err
			}
			fmt.Fprintf(out, "\nSaved %q. Next steps:\n", profileName)
			fmt.Fprintf(out, "  xpc bootstrap --profile %s    # generate cert + PSK, deploy agent\n", profileName)
			fmt.Fprintf(out, "  xpc use %s                     # set as active profile\n", profileName)
			return nil
		},
	}
	c.Flags().StringVar(&name, "name", "", "Profile name to configure (default: --profile or active)")
	return c
}

func promptString(r *bufio.Reader, w io.Writer, label, def string) string {
	if def == "" {
		fmt.Fprintf(w, "%s: ", label)
	} else {
		fmt.Fprintf(w, "%s [%s]: ", label, def)
	}
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return def
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func promptInt(r *bufio.Reader, w io.Writer, label string, def, fallback int) int {
	if def == 0 {
		def = fallback
	}
	for {
		raw := promptString(r, w, label, strconv.Itoa(def))
		n, err := strconv.Atoi(raw)
		if err == nil && n > 0 && n <= 65535 {
			return n
		}
		fmt.Fprintln(w, "  (port must be an integer 1-65535)")
	}
}
