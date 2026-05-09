package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newWatchCmd(g *Globals) *cobra.Command {
	var (
		interval time.Duration
		count    int
		shell    string
	)
	c := &cobra.Command{
		Use:   "watch -- <command> [args...]",
		Short: "Repeat a remote command at an interval, like xpctl watch.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			cmdLine := strings.Join(args, " ")
			run := 0
			for {
				run++
				cmd.Printf("[%d] %s\n", run, time.Now().Format(time.RFC3339))
				stdout, stderr, rc, err := runRemoteCmd(ctx, g, cmdLine, shell)
				if err != nil {
					return err
				}
				cmd.Print(stdout)
				if stderr != "" {
					cmd.PrintErr(stderr)
				}
				if rc != 0 {
					cmd.PrintErrf("(remote rc=%d)\n", rc)
				}
				if count > 0 && run >= count {
					return nil
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(interval):
				}
				_ = fmt.Sprintf // satisfy import for any tooling that strips unused
			}
		},
	}
	c.Flags().DurationVar(&interval, "interval", 2*time.Second, "Time between runs")
	c.Flags().IntVar(&count, "count", 0, "Stop after N runs (0 = forever)")
	c.Flags().StringVar(&shell, "shell", "cmd", "Remote shell: cmd | python | python_file")
	return c
}
