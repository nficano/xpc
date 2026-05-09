package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newCompletionCmd(_ *Globals) *cobra.Command {
	return &cobra.Command{
		Use:       "completion <shell>",
		Short:     "Generate shell completion script for bash, zsh, fish, or powershell.",
		Long:      "Source the output to enable tab completion. See `xpc completion <shell> --help` for shell-specific instructions.",
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletionV2(cmd.OutOrStdout(), true)
			case "zsh":
				return cmd.Root().GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return cmd.Root().GenFishCompletion(cmd.OutOrStdout(), true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletionWithDesc(cmd.OutOrStdout())
			default:
				return wrapUsage(fmt.Errorf("unsupported shell %q", args[0]))
			}
		},
	}
}
