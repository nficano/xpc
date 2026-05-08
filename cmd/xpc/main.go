// Package main is the entry point for the xpc CLI.
//
// xpc is currently a Phase 2 scaffold — only --version, version, and --help
// are wired up. The real subcommand surface (cobra-based) lands in Phase 5.
package main

import (
	"fmt"
	"os"

	"github.com/nficano/xpc/internal/version"
)

const usage = `xpc — Phase 2 scaffold (no commands implemented yet).

Usage:
  xpc --version    Print version and exit.
  xpc --help       Print this message.

See https://github.com/nficano/xpc/blob/main/TASKS.md for project status.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "--version", "-V", "version":
		fmt.Fprintln(stdout, version.String())
		return 0
	case "--help", "-h", "help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "xpc: unknown command %q (Phase 2 scaffold)\n", args[0])
		fmt.Fprint(stderr, usage)
		return 2
	}
}
