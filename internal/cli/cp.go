package cli

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// xpc cp <src> <dst>
//
// Either side may be host:<path> or vm:<path>. Plain paths default to vm: on
// the right and host: on the left, mirroring scp conventions.
//
// v0 implementation: small/medium files (up to ~30 MB after base64 expansion)
// transferred in a single envelope via the existing python shell. A
// chunked/streaming implementation is Phase 5b/6c — for now this matches
// xpctl's file_upload single-shot path.

const cpInlineLimit = 30 * 1024 * 1024 // ~40 MB after base64; well under MaxEnvelopeBytes

func newCpCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "cp <src> <dst>",
		Short: "Copy a file between host and VM (host:/vm: prefixes; default vm:).",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			srcSide, srcPath := splitCpArg(args[0], cpHost)
			dstSide, dstPath := splitCpArg(args[1], cpVM)

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			switch {
			case srcSide == cpHost && dstSide == cpVM:
				return cpUpload(ctx, cmd, g, srcPath, dstPath)
			case srcSide == cpVM && dstSide == cpHost:
				return cpDownload(ctx, cmd, g, srcPath, dstPath)
			case srcSide == cpHost && dstSide == cpHost:
				return wrapUsage(fmt.Errorf("both sides are host:; use a normal `cp` instead"))
			default:
				return wrapUsage(fmt.Errorf("vm:->vm: copy is not supported in v0"))
			}
		},
	}
}

type cpSide int

const (
	cpHost cpSide = iota
	cpVM
)

// splitCpArg parses "host:path", "vm:path", or a bare path. Bare paths use
// the supplied default side. Drive-letter paths like "C:\foo" are recognized
// as VM paths even without the prefix because the colon doesn't kick in
// until index >= 2.
func splitCpArg(s string, def cpSide) (cpSide, string) {
	if strings.HasPrefix(s, "host:") {
		return cpHost, strings.TrimPrefix(s, "host:")
	}
	if strings.HasPrefix(s, "vm:") {
		return cpVM, strings.TrimPrefix(s, "vm:")
	}
	if strings.HasPrefix(s, "remote:") {
		return cpVM, strings.TrimPrefix(s, "remote:")
	}
	// Heuristic: looks like a Windows drive letter ("C:..." with len>=2),
	// which means it's almost certainly a VM path even without prefix.
	if len(s) >= 2 && s[1] == ':' && isDriveLetter(s[0]) {
		return cpVM, s
	}
	return def, s
}

func isDriveLetter(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

func cpUpload(ctx context.Context, cmd *cobra.Command, g *Globals, hostPath, vmPath string) error {
	abs, err := filepath.Abs(hostPath)
	if err != nil {
		return wrapUsage(fmt.Errorf("resolve host path: %w", err))
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return wrapUsage(fmt.Errorf("read %s: %w", abs, err))
	}
	if len(data) > cpInlineLimit {
		return wrapUsage(fmt.Errorf("file %d bytes exceeds the inline cp limit of %d bytes (chunked cp lands in Phase 6c)", len(data), cpInlineLimit))
	}
	encoded := base64.StdEncoding.EncodeToString(data)

	// Python on the VM decodes and writes atomically (write to .tmp, then rename).
	py := fmt.Sprintf(
		"import base64,os\n"+
			"data=base64.b64decode(%[1]q)\n"+
			"path=r%[2]q\n"+
			"d=os.path.dirname(path)\n"+
			"if d and not os.path.isdir(d): os.makedirs(d)\n"+
			"tmp=path+'.xpc.tmp'\n"+
			"with open(tmp,'wb') as f: f.write(data)\n"+
			"if os.path.exists(path): os.remove(path)\n"+
			"os.rename(tmp,path)\n"+
			"print('wrote',len(data),'bytes ->',path)",
		encoded, vmPath)

	stdout, stderr, rc, err := runRemoteCmd(ctx, g, py, "python")
	if err != nil {
		return err
	}
	if err := requireSuccess(stdout, stderr, rc, "cp upload"); err != nil {
		return err
	}
	cmd.Print(strings.TrimRight(stdout, "\r\n") + "\n")
	return nil
}

func cpDownload(ctx context.Context, cmd *cobra.Command, g *Globals, vmPath, hostPath string) error {
	py := fmt.Sprintf(
		"import base64,sys\n"+
			"with open(r%[1]q,'rb') as f: data=f.read()\n"+
			"sys.stdout.write(base64.b64encode(data).decode('ascii'))",
		vmPath)
	stdout, stderr, rc, err := runRemoteCmd(ctx, g, py, "python")
	if err != nil {
		return err
	}
	if err := requireSuccess(stdout, stderr, rc, "cp download"); err != nil {
		return err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(stdout))
	if err != nil {
		return fmt.Errorf("decode remote payload: %w", err)
	}
	abs, err := filepath.Abs(hostPath)
	if err != nil {
		return wrapUsage(fmt.Errorf("resolve host path: %w", err))
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(abs), err)
	}
	if err := os.WriteFile(abs, raw, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", abs, err)
	}
	cmd.Printf("wrote %d bytes -> %s\n", len(raw), abs)
	return nil
}
