// Package main is the entry point for the xpc CLI.
package main

import (
	"os"

	"github.com/nficano/xpc/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr))
}
