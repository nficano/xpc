package cli

import (
	"context"
	"encoding/csv"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nficano/xpc/internal/output"
)

// Process is the minimal per-row shape returned by `tasklist /v /fo csv`.
type Process struct {
	Name      string `json:"name"`
	PID       int    `json:"pid"`
	SessionID string `json:"session_id,omitempty"`
	MemoryKB  int    `json:"memory_kb,omitempty"`
	Status    string `json:"status,omitempty"`
	Username  string `json:"username,omitempty"`
	Title     string `json:"window_title,omitempty"`
}

func newPsCmd(g *Globals) *cobra.Command {
	var filter string
	cmd := &cobra.Command{
		Use:   "ps",
		Short: "List processes on the VM (tasklist /v).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			stdout, stderr, rc, err := runRemoteCmd(ctx, g, "tasklist /v /fo csv /nh", "cmd")
			if err != nil {
				return err
			}
			if err := requireSuccess(stdout, stderr, rc, "tasklist"); err != nil {
				return err
			}

			procs, err := parseTasklistCSV(stdout)
			if err != nil {
				return err
			}
			if filter != "" {
				needle := strings.ToLower(filter)
				kept := procs[:0]
				for _, p := range procs {
					if strings.Contains(strings.ToLower(p.Name), needle) {
						kept = append(kept, p)
					}
				}
				procs = kept
			}

			mode := output.ParseMode(g.OutputMode)
			if mode == output.ModeJSON {
				return output.Encode(cmd.OutOrStdout(), mode, procs)
			}
			headers := []string{"PID", "NAME", "MEMORY_KB", "USER"}
			rows := make([][]any, 0, len(procs))
			for _, p := range procs {
				rows = append(rows, []any{p.PID, p.Name, p.MemoryKB, p.Username})
			}
			return output.EncodeRows(cmd.OutOrStdout(), mode, headers, rows)
		},
	}
	cmd.Flags().StringVar(&filter, "filter", "", "Substring match against the process name (case-insensitive)")
	return cmd
}

// parseTasklistCSV parses the output of `tasklist /v /fo csv /nh`. Columns:
// "Image Name","PID","Session Name","Session#","Mem Usage","Status","User Name","CPU Time","Window Title"
func parseTasklistCSV(text string) ([]Process, error) {
	r := csv.NewReader(strings.NewReader(text))
	r.FieldsPerRecord = -1 // tolerate locale variations
	var procs []Process
	for {
		row, err := r.Read()
		if err != nil {
			break
		}
		if len(row) < 2 {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(row[1]))
		if err != nil {
			continue
		}
		p := Process{Name: strings.TrimSpace(row[0]), PID: pid}
		if len(row) > 2 {
			p.SessionID = strings.TrimSpace(row[2])
		}
		if len(row) > 4 {
			// "Mem Usage" looks like "12,345 K"; strip everything non-digit.
			p.MemoryKB = parseMemKB(row[4])
		}
		if len(row) > 5 {
			p.Status = strings.TrimSpace(row[5])
		}
		if len(row) > 6 {
			p.Username = strings.TrimSpace(row[6])
		}
		if len(row) > 8 {
			p.Title = strings.TrimSpace(row[8])
		}
		procs = append(procs, p)
	}
	return procs, nil
}

func parseMemKB(s string) int {
	digits := 0
	got := false
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits = digits*10 + int(r-'0')
			got = true
		} else if got && (r == ',' || r == '.' || r == ' ') {
			continue
		} else if got {
			break
		}
	}
	return digits
}
