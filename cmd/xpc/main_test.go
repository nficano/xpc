package main

import (
	"testing"

	"github.com/nficano/xpc/internal/version"
)

// TestVersionAvailable checks that the version package is wired up so the
// scaffold actually compiles and links across packages.
func TestVersionAvailable(t *testing.T) {
	t.Parallel()
	got := version.String()
	if got == "" {
		t.Fatal("version.String() returned empty; expected a non-empty default")
	}
}
