package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nficano/xpc/internal/cli"
)

func TestVersionCommand(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	rc := cli.Execute([]string{"version"}, &stdout, &stderr)
	if rc != 0 {
		t.Fatalf("rc = %d; want 0 (stderr: %s)", rc, stderr.String())
	}
	if !strings.Contains(stdout.String(), "0.0.0-dev") {
		t.Fatalf("stdout = %q; expected version string", stdout.String())
	}
}

func TestRootHelp(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	rc := cli.Execute([]string{"--help"}, &stdout, &stderr)
	if rc != 0 {
		t.Fatalf("rc = %d; want 0", rc)
	}
	for _, want := range []string{"configure", "exec", "completion", "profile", "agent"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestUnknownSubcommandFails(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	rc := cli.Execute([]string{"definitelynotacommand"}, &stdout, &stderr)
	if rc == 0 {
		t.Fatalf("rc = 0 for unknown command; want non-zero (stdout=%q stderr=%q)", stdout.String(), stderr.String())
	}
}
