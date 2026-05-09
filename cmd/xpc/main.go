// Package main is the entry point for the xpc CLI.
package main

import (
	"os"

	"github.com/nficano/xpc/internal/cli"
)

func main() {
	os.Exit(cli.Execute(cli.ShimArgs(os.Args), os.Stdout, os.Stderr))
}
