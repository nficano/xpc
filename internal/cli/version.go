package cli

import (
	"github.com/spf13/cobra"

	"github.com/nficano/xpc/internal/version"
)

func newVersionCmd(_ *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the xpc client version.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.Println(version.String())
			return nil
		},
	}
}
