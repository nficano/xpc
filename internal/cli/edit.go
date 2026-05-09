package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

// xpc edit <vm:path>
//
// Pulls the file via xpc cp into a host tempfile, runs $EDITOR on it,
// pushes back to the VM if the file changed. Mirrors xpctl's edit command.

func newEditCmd(g *Globals) *cobra.Command {
	var editor string
	c := &cobra.Command{
		Use:   "edit <vm:path>",
		Short: "Pull a remote file, edit it locally with $EDITOR, push the result back if changed.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			vmPath := stripVMPrefix(args[0])
			ed := editor
			if ed == "" {
				ed = os.Getenv("EDITOR")
			}
			if ed == "" {
				return wrapUsage(fmt.Errorf("set --editor or $EDITOR"))
			}

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			tmp, err := os.CreateTemp("", "xpc-edit-*"+filepath.Ext(vmPath))
			if err != nil {
				return err
			}
			tmpPath := tmp.Name()
			_ = tmp.Close()
			defer func() { _ = os.Remove(tmpPath) }()

			if err := cpDownload(ctx, cmd, g, vmPath, tmpPath); err != nil {
				return err
			}
			before, err := os.ReadFile(tmpPath)
			if err != nil {
				return err
			}

			run := exec.Command("sh", "-c", ed+" "+shellQuote(tmpPath))
			run.Stdin = os.Stdin
			run.Stdout = os.Stdout
			run.Stderr = os.Stderr
			if err := run.Run(); err != nil {
				return fmt.Errorf("editor %q exited: %w", ed, err)
			}

			after, err := os.ReadFile(tmpPath)
			if err != nil {
				return err
			}
			if bytes.Equal(before, after) {
				cmd.Println("no changes")
				return nil
			}
			cmd.Printf("changed (%d -> %d bytes); pushing back\n", len(before), len(after))
			return cpUpload(ctx, cmd, g, tmpPath, vmPath)
		},
	}
	c.Flags().StringVar(&editor, "editor", "", "Editor command (default: $EDITOR)")
	return c
}

func shellQuote(s string) string {
	return "'" + replaceAll(s, "'", `'\''`) + "'"
}

func replaceAll(s, old, new string) string {
	out := s
	for {
		next := indexOf(out, old)
		if next < 0 {
			return out
		}
		out = out[:next] + new + out[next+len(old):]
	}
}

func indexOf(s, sub string) int {
	if len(sub) == 0 {
		return 0
	}
	if len(sub) > len(s) {
		return -1
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
