package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"
	"gopkg.in/ini.v1"

	"github.com/nficano/xpc/internal/profile"
)

func newMigrateCmd(_ *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "migrate-from-xpctl",
		Short: "Read ~/.xpcli/config and write equivalent ~/.xpc/{config,credentials} entries.",
		Long: `Migrates xpctl-style profiles into xpc's AWS-style split config. The
fingerprint and PSK are NOT migrated (xpctl had neither); run
` + "`xpc bootstrap`" + ` after migration to deploy the new agent and pin both.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			src := filepath.Join(home, ".xpcli", "config")
			if _, err := os.Stat(src); err != nil {
				return wrapUsage(fmt.Errorf("no xpctl config at %s", src))
			}
			f, err := ini.Load(src)
			if err != nil {
				return fmt.Errorf("parse %s: %w", src, err)
			}

			migrated := 0
			for _, sec := range f.Sections() {
				name := sec.Name()
				if name == ini.DefaultSection {
					continue
				}
				if sec.Key("hostname").String() == "" {
					continue
				}
				port, _ := strconv.Atoi(sec.Key("port").String())
				if port == 0 {
					port = 9578
				}
				p := &profile.Profile{
					Name:          name,
					Host:          sec.Key("hostname").String(),
					Port:          port,
					SSHUser:       sec.Key("username").String(),
					SSHPassword:   sec.Key("password").String(),
					VerifyHostKey: true,
				}
				if err := profile.Save(p); err != nil {
					return fmt.Errorf("save profile %s: %w", name, err)
				}
				cmd.Printf("migrated %s -> ~/.xpc/{config,credentials}\n", name)
				migrated++
			}
			if migrated == 0 {
				cmd.Println("(no xpctl profiles found)")
			} else {
				cmd.Printf("\n%d profile(s) migrated. Next:\n", migrated)
				cmd.Println("  xpc bootstrap --profile <name>     # deploy agent + pin fingerprint + generate PSK")
			}
			return nil
		},
	}
}
