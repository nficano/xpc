package cli

import (
	"path/filepath"
	"strings"
)

// shims maps the recognised argv[0] basenames to the xpc subcommand they
// should dispatch to. When `xpc` is invoked under one of these names
// (typically a symlink: `ln -s xpc xpcexec`), the matching subcommand is
// prepended to the argument list.
//
// New shims live here; nothing else needs to change.
var shims = map[string]string{
	"xpcexec":  "exec",
	"xpcreg":   "reg",
	"xpcps":    "ps",
	"xpcsvc":   "svc",
	"xpccp":    "cp",
	"xpcshot":  "shot",
	"xpcsend":  "send",
	"xpcdump":  "dump",
	"xpcinj":   "inj",
	"xpcdll":   "dll",
	"xpcbat":   "bat",
	"xpcevt":   "evt",
	"xpcenv":   "env",
	"xpcboot":  "boot",
	"xpcpy":    "py",
	"xpctun":   "tun",
	"xpctrace": "trace",
	"xpcdbg":   "dbg",
	"xpcsnap":  "snap",
	"xpcfetch": "fetch",
	"xpcedit":  "edit",
	"xpccat":   "cat",
	"xpchead":  "head",
	"xpctail":  "tail",
	"xpcfind":  "find",
	"xpcsum":   "sum",
	"xpcwatch": "watch",
	"xpcinfo":  "info",
	"xpcnet":   "net",
}

// ShimArgs inspects argv[0] and, if it matches a registered shim,
// returns argv with the corresponding subcommand prepended. Otherwise
// it returns argv[1:] unchanged. Pass os.Args.
func ShimArgs(argv []string) []string {
	if len(argv) == 0 {
		return nil
	}
	base := strings.ToLower(filepath.Base(argv[0]))
	base = strings.TrimSuffix(base, ".exe")
	if sub, ok := shims[base]; ok {
		out := make([]string, 0, len(argv))
		out = append(out, sub)
		out = append(out, argv[1:]...)
		return out
	}
	return argv[1:]
}
