package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newEvtCmd(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "evt",
		Short: "Windows event log queries (XP-specific eventquery.vbs wrapper).",
	}
	cmd.AddCommand(newEvtQueryCmd(g))
	cmd.AddCommand(newEvtTailCmd(g))
	cmd.PersistentFlags().String("log", "Application", "Event log name (Application, System, Security, ...)")
	cmd.PersistentFlags().Int("max", 20, "Maximum number of records to return")
	cmd.PersistentFlags().String("type", "", "Filter by record type: Error | Warning | Information | ...")
	return cmd
}

func newEvtQueryCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
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
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			out, err := runEventQuery(ctx, g, logName, etype, maxN)
			if err != nil {
				return err
			}
			cmd.Print(out)
			return nil
		},
	}
}

func newEvtTailCmd(g *Globals) *cobra.Command {
	var interval time.Duration
	c := &cobra.Command{
		Use:   "tail",
		Short: "Poll the event log and stream new entries as they appear.",
		Long: `Like ` + "`tail -f`" + ` for the Windows event log. Polls eventquery.vbs at
--interval (default 5s) and prints only records that weren't seen on the
previous poll. Press Ctrl-C to exit.

The first poll prints the existing tail (last --max records); subsequent
polls only print new arrivals.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			logName, _ := cmd.Flags().GetString("log")
			maxN, _ := cmd.Flags().GetInt("max")
			etype, _ := cmd.Flags().GetString("type")
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			seen := make(map[string]struct{}, 256)
			first := true
			for {
				out, err := runEventQuery(ctx, g, logName, etype, maxN)
				if err != nil {
					if errors.Is(err, context.Canceled) {
						return nil
					}
					return err
				}
				printed, fresh := dedupeEvtLines(out, seen)
				for _, line := range fresh {
					seen[line] = struct{}{}
				}
				if first || printed != "" {
					cmd.Print(printed)
				}
				first = false
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(interval):
				}
			}
		},
	}
	c.Flags().DurationVar(&interval, "interval", 5*time.Second, "Poll interval")
	return c
}

// runEventQuery runs eventquery.vbs once and returns the captured stdout.
func runEventQuery(ctx context.Context, g *Globals, logName, etype string, maxN int) (string, error) {
	parts := []string{`cscript /nologo C:\WINDOWS\system32\eventquery.vbs`}
	if logName != "" {
		parts = append(parts, "/L", quoteRegArg(logName))
	}
	if maxN > 0 {
		parts = append(parts, "/R", fmt.Sprintf("%d", maxN))
	}
	if etype != "" {
		parts = append(parts, "/FI", quoteRegArg("Type eq "+etype))
	}
	stdout, stderr, rc, err := runRemoteCmd(ctx, g, strings.Join(parts, " "), "cmd")
	if err != nil {
		return "", err
	}
	if err := requireSuccess(stdout, stderr, rc, "eventquery.vbs"); err != nil {
		return "", err
	}
	return stdout, nil
}

// dedupeEvtLines splits the eventquery output into header + records and
// returns (printable_block, fresh_record_lines). Header/banner lines are
// preserved on the first call but suppressed afterward (caller passes a
// `seen` set that grows monotonically).
//
// "Fresh" is determined per-line: if the trimmed line isn't in `seen` and
// looks like a record row (starts with a non-whitespace token followed by
// whitespace), it's considered new and emitted.
func dedupeEvtLines(out string, seen map[string]struct{}) (string, []string) {
	var (
		printed strings.Builder
		fresh   []string
	)
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !looksLikeEvtRecord(trimmed) {
			// Header/banner: only print on the first poll (before `seen`
			// has been populated with anything).
			if len(seen) == 0 {
				printed.WriteString(line)
				printed.WriteByte('\n')
			}
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		fresh = append(fresh, trimmed)
		printed.WriteString(line)
		printed.WriteByte('\n')
	}
	return printed.String(), fresh
}

// looksLikeEvtRecord recognizes record rows from eventquery output.
// They start with one of the known type tokens.
func looksLikeEvtRecord(s string) bool {
	for _, prefix := range []string{"Information ", "Warning ", "Error ", "Success Audit ", "Failure Audit "} {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}
