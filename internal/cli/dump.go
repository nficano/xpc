package cli

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// xpc dump <pid> [<host-path>]
//
// MiniDumpWriteDump via dbghelp.dll. Defaults to MiniDumpNormal; --full
// flips to MiniDumpWithFullMemory. Bytes are transferred back via base64.

func newDumpCmd(g *Globals) *cobra.Command {
	var (
		full    bool
		outPath string
	)
	c := &cobra.Command{
		Use:   "dump <pid> [<host-path>]",
		Short: "MiniDump a process and pull the .dmp back to the host.",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, err := atoi(args[0])
			if err != nil {
				return wrapUsage(err)
			}

			dst := outPath
			if dst == "" && len(args) >= 2 {
				dst = args[1]
			}
			if dst == "" {
				home, _ := os.UserHomeDir()
				dst = filepath.Join(home, ".xpc", "dumps",
					fmt.Sprintf("pid-%d-%s.dmp", pid, time.Now().UTC().Format("20060102T150405Z")))
			}

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			py := minidumpPython(pid, full)
			stdout, stderr, rc, err := runRemoteCmd(ctx, g, py, "python")
			if err != nil {
				return err
			}
			if rc != 0 {
				return &RemoteError{
					error:    fmt.Errorf("dump failed (rc=%d): %s", rc, strings.TrimSpace(stderr)),
					ExitCode: rc,
				}
			}
			raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(stdout))
			if err != nil {
				return fmt.Errorf("decode dump payload: %w", err)
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(dst, raw, 0o644); err != nil {
				return fmt.Errorf("write %s: %w", dst, err)
			}
			cmd.Printf("wrote %d bytes -> %s\n", len(raw), dst)
			return nil
		},
	}
	c.Flags().BoolVar(&full, "full", false, "Use MiniDumpWithFullMemory (much larger)")
	c.Flags().StringVarP(&outPath, "output", "o", "", "Local path for the .dmp (default: ~/.xpc/dumps/pid-<n>-<utc>.dmp)")
	return c
}

func minidumpPython(pid int, full bool) string {
	mdt := "0x00000000" // MiniDumpNormal
	if full {
		mdt = "0x00000002" // MiniDumpWithFullMemory
	}
	return fmt.Sprintf(`
import ctypes, base64, os, sys, tempfile
from ctypes import wintypes

PROCESS_ALL_ACCESS = 0x1F0FFF
GENERIC_WRITE      = 0x40000000
CREATE_ALWAYS      = 2

kernel32 = ctypes.windll.kernel32
dbghelp  = ctypes.windll.dbghelp
kernel32.OpenProcess.restype = wintypes.HANDLE
kernel32.CreateFileW.restype = wintypes.HANDLE
dbghelp.MiniDumpWriteDump.restype = wintypes.BOOL

h = kernel32.OpenProcess(PROCESS_ALL_ACCESS, False, %[1]d)
if not h:
    sys.stderr.write("OpenProcess: " + str(ctypes.WinError()) + "\n"); sys.exit(2)

tmp = tempfile.NamedTemporaryFile(prefix="xpc-dmp-", suffix=".dmp", delete=False)
tmp.close()
tmp_path = tmp.name

fh = kernel32.CreateFileW(tmp_path, GENERIC_WRITE, 0, None, CREATE_ALWAYS, 0, None)
if fh == wintypes.HANDLE(-1).value or fh == 0:
    sys.stderr.write("CreateFileW: " + str(ctypes.WinError()) + "\n"); sys.exit(3)

ok = dbghelp.MiniDumpWriteDump(h, %[1]d, fh, %[2]s, None, None, None)
kernel32.CloseHandle(fh)
kernel32.CloseHandle(h)
if not ok:
    sys.stderr.write("MiniDumpWriteDump: " + str(ctypes.WinError()) + "\n"); sys.exit(4)

with open(tmp_path, "rb") as f:
    data = f.read()
os.remove(tmp_path)
sys.stdout.write(base64.b64encode(data).decode("ascii"))
`, pid, mdt)
}
