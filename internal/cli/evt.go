package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

func newEvtCmd(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "evt",
		Short: "Windows event log queries (XP-specific eventquery.vbs wrapper).",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "query",
		Short: "Run eventquery.vbs to fetch recent log entries.",
		Long: `Windows XP ships eventquery.vbs at C:\WINDOWS\system32\eventquery.vbs.
Common usage:
  xpc evt query --log Application --max 20
  xpc evt query --log System      --type Error
`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			logName, _ := cmd.Flags().GetString("log")
			maxN, _ := cmd.Flags().GetInt("max")
			etype, _ := cmd.Flags().GetString("type")

			parts := []string{"cscript /nologo C:\\WINDOWS\\system32\\eventquery.vbs"}
			if logName != "" {
				parts = append(parts, "/L", quoteRegArg(logName))
			}
			if maxN > 0 {
				parts = append(parts, "/R", fmt.Sprintf("%d", maxN))
			}
			if etype != "" {
				parts = append(parts, "/FI", quoteRegArg("Type eq "+etype))
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			line := join(parts)
			stdout, stderr, rc, err := runRemoteCmd(ctx, g, line, "cmd")
			if err != nil {
				return err
			}
			if err := requireSuccess(stdout, stderr, rc, "eventquery.vbs"); err != nil {
				return err
			}
			cmd.Print(stdout)
			return nil
		},
	})
	cmd.PersistentFlags().String("log", "Application", "Event log name (Application, System, Security, ...)")
	cmd.PersistentFlags().Int("max", 20, "Maximum number of records to return")
	cmd.PersistentFlags().String("type", "", "Filter by record type: Error | Warning | Information | ...")
	return cmd
}

func join(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}
