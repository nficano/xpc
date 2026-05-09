package cli

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// xpc cp <src> <dst>
//
// Either side may be host:<path> or vm:<path>. Plain paths default to vm: on
// the right and host: on the left, mirroring scp conventions.
//
// Files <= cpInlineLimit are transferred in a single envelope (matches
// xpctl's file_upload single-shot path). Files larger than that are
// chunked across multiple `exec` invocations: the agent appends to a .tmp
// file on upload and seeks into the source on download.

// cpInlineLimit is the file-size cutoff (in raw bytes) below which cp
// uses a single envelope. Above this it falls through to chunked I/O.
const cpInlineLimit = 30 * 1024 * 1024 // ~40 MB after base64; well under MaxEnvelopeBytes

// cpChunkBytes is the raw-byte chunk size used for the chunked path.
// 8 MB raw -> ~10.7 MB base64; comfortably below the envelope ceiling.
const cpChunkBytes = 8 * 1024 * 1024

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
	st, err := os.Stat(abs)
	if err != nil {
		return wrapUsage(fmt.Errorf("stat %s: %w", abs, err))
	}
	if st.Size() <= cpInlineLimit {
		data, err := os.ReadFile(abs)
		if err != nil {
			return wrapUsage(fmt.Errorf("read %s: %w", abs, err))
		}
		return cpUploadInline(ctx, cmd, g, data, vmPath)
	}
	return cpUploadChunked(ctx, cmd, g, abs, vmPath, st.Size())
}

func cpUploadInline(ctx context.Context, cmd *cobra.Command, g *Globals, data []byte, vmPath string) error {
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

// cpUploadChunked streams a large file as cpChunkBytes-sized chunks. Each
// chunk is sent through a separate `exec` invocation:
//   - first chunk: truncate-write to <vmPath>.xpc.tmp
//   - middle chunks: append-write to <vmPath>.xpc.tmp
//   - final chunk: append + rename .xpc.tmp -> vmPath atomically
//
// A SHA-256 of the source is computed on the host and verified on the VM
// in the rename step; mismatch => the temp file stays in place and the
// final rename is aborted with a non-zero exit.
func cpUploadChunked(ctx context.Context, cmd *cobra.Command, g *Globals, hostPath, vmPath string, total int64) error {
	f, err := os.Open(hostPath)
	if err != nil {
		return wrapUsage(fmt.Errorf("open %s: %w", hostPath, err))
	}
	defer func() { _ = f.Close() }()

	hasher := sha256.New()
	buf := make([]byte, cpChunkBytes)
	var offset int64
	chunkIndex := 0
	for offset < total {
		n, rerr := f.Read(buf)
		if n == 0 {
			if rerr != nil {
				return wrapUsage(fmt.Errorf("read %s: %w", hostPath, rerr))
			}
			break
		}
		hasher.Write(buf[:n])
		isFirst := chunkIndex == 0
		isLast := offset+int64(n) >= total
		if err := cpUploadChunk(ctx, cmd, g, vmPath, buf[:n], isFirst, isLast, total, hex.EncodeToString(hasher.Sum(nil))); err != nil {
			return err
		}
		offset += int64(n)
		chunkIndex++
		cmd.Printf("\rcp: %d/%d bytes (%d%%)", offset, total, 100*offset/total)
		if rerr != nil {
			break
		}
	}
	cmd.Println()
	return nil
}

func cpUploadChunk(ctx context.Context, cmd *cobra.Command, g *Globals, vmPath string, chunk []byte, isFirst, isLast bool, total int64, fullSha256 string) error {
	encoded := base64.StdEncoding.EncodeToString(chunk)
	mode := "ab"
	if isFirst {
		mode = "wb"
	}
	verifyAndRename := ""
	if isLast {
		verifyAndRename = fmt.Sprintf(
			"import hashlib\n"+
				"h=hashlib.sha256()\n"+
				"with open(tmp,'rb') as g:\n"+
				"  while True:\n"+
				"    b=g.read(65536)\n"+
				"    if not b: break\n"+
				"    h.update(b)\n"+
				"got=h.hexdigest()\n"+
				"want=%[1]q\n"+
				"if got!=want:\n"+
				"  raise SystemExit('sha256 mismatch host=%%s vm=%%s'%%(want,got))\n"+
				"if os.path.exists(path): os.remove(path)\n"+
				"os.rename(tmp,path)\n"+
				"print('wrote',%[2]d,'bytes ->',path)\n",
			fullSha256, total)
	}
	py := fmt.Sprintf(
		"import base64,os\n"+
			"path=r%[1]q\n"+
			"tmp=path+'.xpc.tmp'\n"+
			"d=os.path.dirname(path)\n"+
			"if d and not os.path.isdir(d): os.makedirs(d)\n"+
			"data=base64.b64decode(%[2]q)\n"+
			"with open(tmp,%[3]q) as f: f.write(data)\n"+
			"%[4]s",
		vmPath, encoded, mode, verifyAndRename)

	stdout, stderr, rc, err := runRemoteCmd(ctx, g, py, "python")
	if err != nil {
		return err
	}
	if err := requireSuccess(stdout, stderr, rc, "cp upload chunk"); err != nil {
		return err
	}
	if isLast {
		cmd.Println()
		cmd.Print(strings.TrimRight(stdout, "\r\n") + "\n")
	}
	return nil
}

func cpDownload(ctx context.Context, cmd *cobra.Command, g *Globals, vmPath, hostPath string) error {
	abs, err := filepath.Abs(hostPath)
	if err != nil {
		return wrapUsage(fmt.Errorf("resolve host path: %w", err))
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(abs), err)
	}
	// Probe size + sha256 in one call so we know which path to take and can
	// verify at the end of a chunked transfer.
	probePy := fmt.Sprintf(
		"import os,hashlib,sys\n"+
			"path=r%[1]q\n"+
			"size=os.path.getsize(path)\n"+
			"h=hashlib.sha256()\n"+
			"with open(path,'rb') as f:\n"+
			"  while True:\n"+
			"    b=f.read(65536)\n"+
			"    if not b: break\n"+
			"    h.update(b)\n"+
			"sys.stdout.write('%%d %%s'%%(size,h.hexdigest()))",
		vmPath)
	stdout, stderr, rc, err := runRemoteCmd(ctx, g, probePy, "python")
	if err != nil {
		return err
	}
	if err := requireSuccess(stdout, stderr, rc, "cp download probe"); err != nil {
		return err
	}
	parts := strings.Fields(strings.TrimSpace(stdout))
	if len(parts) != 2 {
		return fmt.Errorf("probe returned unexpected output: %q", stdout)
	}
	total, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return fmt.Errorf("parse remote file size: %w", err)
	}
	wantSha := parts[1]

	if total <= cpInlineLimit {
		return cpDownloadInline(ctx, cmd, g, vmPath, abs, total, wantSha)
	}
	return cpDownloadChunked(ctx, cmd, g, vmPath, abs, total, wantSha)
}

func cpDownloadInline(ctx context.Context, cmd *cobra.Command, g *Globals, vmPath, abs string, total int64, wantSha string) error {
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
	if int64(len(raw)) != total {
		return fmt.Errorf("size mismatch: got %d bytes, expected %d", len(raw), total)
	}
	gotSha := hex.EncodeToString(sha256.New().Sum(nil)) // placeholder; recompute below
	h := sha256.New()
	_, _ = h.Write(raw)
	gotSha = hex.EncodeToString(h.Sum(nil))
	if gotSha != wantSha {
		return fmt.Errorf("sha256 mismatch: vm=%s host=%s", wantSha, gotSha)
	}
	if err := os.WriteFile(abs, raw, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", abs, err)
	}
	cmd.Printf("wrote %d bytes -> %s\n", len(raw), abs)
	return nil
}

func cpDownloadChunked(ctx context.Context, cmd *cobra.Command, g *Globals, vmPath, abs string, total int64, wantSha string) error {
	tmpPath := abs + ".xpc.tmp"
	out, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open temp %s: %w", tmpPath, err)
	}
	hasher := sha256.New()
	defer func() { _ = out.Close() }()

	var offset int64
	for offset < total {
		py := fmt.Sprintf(
			"import base64,sys\n"+
				"with open(r%[1]q,'rb') as f:\n"+
				"  f.seek(%[2]d)\n"+
				"  data=f.read(%[3]d)\n"+
				"sys.stdout.write(base64.b64encode(data).decode('ascii'))",
			vmPath, offset, cpChunkBytes)
		stdout, stderr, rc, err := runRemoteCmd(ctx, g, py, "python")
		if err != nil {
			return err
		}
		if err := requireSuccess(stdout, stderr, rc, "cp download chunk"); err != nil {
			return err
		}
		raw, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(stdout))
		if derr != nil {
			return fmt.Errorf("decode chunk: %w", derr)
		}
		if len(raw) == 0 {
			return fmt.Errorf("chunk at offset %d returned empty", offset)
		}
		if _, err := out.Write(raw); err != nil {
			return fmt.Errorf("write temp: %w", err)
		}
		hasher.Write(raw)
		offset += int64(len(raw))
		cmd.Printf("\rcp: %d/%d bytes (%d%%)", offset, total, 100*offset/total)
	}
	cmd.Println()
	if err := out.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	gotSha := hex.EncodeToString(hasher.Sum(nil))
	if gotSha != wantSha {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("sha256 mismatch: vm=%s host=%s", wantSha, gotSha)
	}
	if err := os.Rename(tmpPath, abs); err != nil {
		return fmt.Errorf("rename temp: %w", err)
	}
	cmd.Printf("wrote %d bytes -> %s\n", total, abs)
	return nil
}
