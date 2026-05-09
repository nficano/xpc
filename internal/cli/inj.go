package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// xpc inj <pid> <vm:dll-path>
//
// Classic Win32 DLL injection: OpenProcess + VirtualAllocEx + WriteProcessMemory
// + CreateRemoteThread(LoadLibraryA). Runs through --shell python so we don't
// have to teach the agent a new tool. Adapted from xpctl/assets/scripts/
// dll_inject.py.

func newInjCmd(g *Globals) *cobra.Command {
	c := &cobra.Command{
		Use:   "inj <pid> <vm:dll-path>",
		Short: "Inject a DLL into a process via CreateRemoteThread + LoadLibraryA.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, err := atoi(args[0])
			if err != nil {
				return wrapUsage(fmt.Errorf("invalid pid: %v", err))
			}
			dllPath := stripVMPrefix(args[1])
			if g.DryRun {
				cmd.Printf("(dry-run) inject %s into pid %d\n", dllPath, pid)
				return nil
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			py := injectionPython(pid, dllPath)
			stdout, stderr, rc, err := runRemoteCmd(ctx, g, py, "python")
			if err != nil {
				return err
			}
			cmd.Print(stdout)
			cmd.PrintErr(stderr)
			if rc != 0 {
				return &RemoteError{
					error:    fmt.Errorf("inject failed (rc=%d): %s", rc, strings.TrimSpace(stderr)),
					ExitCode: rc,
				}
			}
			return nil
		},
	}
	return c
}

func injectionPython(pid int, dllPath string) string {
	return fmt.Sprintf(`
import ctypes, sys
from ctypes import wintypes

PROCESS_ALL_ACCESS = 0x1F0FFF
MEM_COMMIT_RESERVE = 0x3000
PAGE_RW            = 0x04

kernel32 = ctypes.windll.kernel32
kernel32.OpenProcess.restype  = wintypes.HANDLE
kernel32.VirtualAllocEx.restype = ctypes.c_void_p
kernel32.GetProcAddress.restype = ctypes.c_void_p
kernel32.CreateRemoteThread.restype = wintypes.HANDLE

dll = ("%[1]s").encode("ascii") + b"\x00"

h = kernel32.OpenProcess(PROCESS_ALL_ACCESS, False, %[2]d)
if not h:
    sys.stderr.write("OpenProcess: " + str(ctypes.WinError()) + "\n"); sys.exit(2)

addr = kernel32.VirtualAllocEx(h, None, len(dll), MEM_COMMIT_RESERVE, PAGE_RW)
if not addr:
    sys.stderr.write("VirtualAllocEx: " + str(ctypes.WinError()) + "\n"); sys.exit(3)

written = ctypes.c_size_t(0)
ok = kernel32.WriteProcessMemory(h, ctypes.c_void_p(addr), dll, len(dll), ctypes.byref(written))
if not ok:
    sys.stderr.write("WriteProcessMemory: " + str(ctypes.WinError()) + "\n"); sys.exit(4)

mod = kernel32.GetModuleHandleA(b"kernel32.dll")
load = kernel32.GetProcAddress(mod, b"LoadLibraryA")
if not load:
    sys.stderr.write("GetProcAddress(LoadLibraryA): " + str(ctypes.WinError()) + "\n"); sys.exit(5)

th = kernel32.CreateRemoteThread(h, None, 0, ctypes.c_void_p(load), ctypes.c_void_p(addr), 0, None)
if not th:
    sys.stderr.write("CreateRemoteThread: " + str(ctypes.WinError()) + "\n"); sys.exit(6)

print("injected", "%[1]s", "into pid", %[2]d)
`, escapeForPyDoubleQuote(dllPath), pid)
}

// escapeForPyDoubleQuote prepares a string for Python "..." literal use.
// Replaces backslashes and double-quotes only.
func escapeForPyDoubleQuote(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			out = append(out, '\\', '\\')
		case '"':
			out = append(out, '\\', '"')
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}

func atoi(s string) (int, error) {
	n := 0
	if len(s) == 0 {
		return 0, fmt.Errorf("empty")
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not a number: %q", s)
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}
