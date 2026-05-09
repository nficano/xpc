package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// fs.go gathers the small filesystem helpers ported from xpctl: cat, head,
// tail, find, sum. Each is a thin wrapper around either cmd.exe or a tiny
// python-shell snippet. They share the same exec-via-runRemoteCmd path so
// behavior is consistent with xpc exec.

func newCatCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "cat <vm:path>",
		Short: "Print a remote file.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := stripVMPrefix(args[0])
			py := fmt.Sprintf(
				"import sys\n"+
					"with open(r%q,'rb') as f:\n"+
					"  data=f.read()\n"+
					"  try:\n"+
					"    sys.stdout.buffer.write(data)\n"+
					"  except AttributeError:\n"+
					"    sys.stdout.write(data.decode('utf-8','replace'))",
				path)
			return runFsPython(cmd, g, py, "cat")
		},
	}
}

func newHeadCmd(g *Globals) *cobra.Command {
	var n int
	c := &cobra.Command{
		Use:   "head <vm:path>",
		Short: "Print the first N lines of a remote file.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := stripVMPrefix(args[0])
			py := fmt.Sprintf(
				"f=open(r%[1]q,'rb')\n"+
					"for i,l in enumerate(f):\n"+
					"    if i>=%[2]d: break\n"+
					"    import sys; sys.stdout.write(l.decode('utf-8','replace'))",
				path, n)
			return runFsPython(cmd, g, py, "head")
		},
	}
	c.Flags().IntVarP(&n, "lines", "n", 20, "Number of lines")
	return c
}

func newTailCmd(g *Globals) *cobra.Command {
	var n int
	c := &cobra.Command{
		Use:   "tail <vm:path>",
		Short: "Print the last N lines of a remote file.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := stripVMPrefix(args[0])
			py := fmt.Sprintf(
				"import collections,sys\n"+
					"buf=collections.deque(open(r%[1]q,'rb'),maxlen=%[2]d)\n"+
					"for l in buf: sys.stdout.write(l.decode('utf-8','replace'))",
				path, n)
			return runFsPython(cmd, g, py, "tail")
		},
	}
	c.Flags().IntVarP(&n, "lines", "n", 20, "Number of lines")
	return c
}

func newFindCmd(g *Globals) *cobra.Command {
	var (
		glob, regex string
	)
	c := &cobra.Command{
		Use:   "find <vm:path>",
		Short: "Recursively find files by glob and/or regex.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := stripVMPrefix(args[0])
			py := fmt.Sprintf(
				"import os,fnmatch,re\n"+
					"glob=%[2]q\n"+
					"rx=re.compile(%[3]q) if %[3]q else None\n"+
					"for d,_,fs in os.walk(r%[1]q):\n"+
					"  for fn in fs:\n"+
					"    full=os.path.join(d,fn)\n"+
					"    if glob and not fnmatch.fnmatch(fn,glob): continue\n"+
					"    if rx and not rx.search(full): continue\n"+
					"    print(full)",
				root, glob, regex)
			return runFsPython(cmd, g, py, "find")
		},
	}
	c.Flags().StringVar(&glob, "glob", "*", "Glob pattern matched against the basename")
	c.Flags().StringVar(&regex, "regex", "", "Optional regex matched against the full path")
	return c
}

func newSumCmd(g *Globals) *cobra.Command {
	var algo string
	c := &cobra.Command{
		Use:   "sum <vm:path>",
		Short: "Compute md5 / sha1 / sha256 of a remote file.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !validAlgo(algo) {
				return wrapUsage(fmt.Errorf("--algo must be md5, sha1, or sha256"))
			}
			path := stripVMPrefix(args[0])
			py := fmt.Sprintf(
				"import hashlib\n"+
					"h=hashlib.new(%[2]q)\n"+
					"with open(r%[1]q,'rb') as f:\n"+
					"  for chunk in iter(lambda: f.read(65536), b''):\n"+
					"    h.update(chunk)\n"+
					"print(h.hexdigest()+'  '+r%[1]q)",
				path, algo)
			return runFsPython(cmd, g, py, "sum")
		},
	}
	c.Flags().StringVar(&algo, "algo", "sha256", "Hash algorithm: md5 | sha1 | sha256")
	return c
}

// stripVMPrefix removes the leading "vm:" / "remote:" prefix that some
// xpctl-flavored callers may include. Plain paths are returned unchanged.
func stripVMPrefix(p string) string {
	for _, pre := range []string{"vm:", "remote:"} {
		if strings.HasPrefix(p, pre) {
			return p[len(pre):]
		}
	}
	return p
}

func validAlgo(a string) bool {
	switch a {
	case "md5", "sha1", "sha256":
		return true
	}
	return false
}

func runFsPython(cmd *cobra.Command, g *Globals, py, what string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	stdout, stderr, rc, err := runRemoteCmd(ctx, g, py, "python")
	if err != nil {
		return err
	}
	if err := requireSuccess(stdout, stderr, rc, what); err != nil {
		return err
	}
	cmd.Print(stdout)
	return nil
}
