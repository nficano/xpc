package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
)

// runRemoteCmd invokes the remote `exec` tool with the given command and
// returns (stdout, stderr, exitCode, error). For commands whose output is
// compact (systeminfo, netstat, etc.) this is more convenient than
// streaming directly to the terminal.
//
// The command runs through whichever shell is requested; "cmd" wraps in
// cmd.exe /c, "python" runs as python -c source, "python_file" runs a
// .py file already on the VM.
func runRemoteCmd(ctx context.Context, g *Globals, cmd, shell string) (string, string, int, error) {
	p, err := g.ResolveProfile()
	if err != nil {
		return "", "", 0, err
	}
	conn, sid, err := dialAndOpen(p, g.Timeout)
	if err != nil {
		return "", "", 0, err
	}
	defer func() {
		closeSession(conn, p.PSK, sid)
		_ = conn.Close()
	}()

	var stdout, stderr bytes.Buffer
	rc, err := invokeExec(ctx, conn, p.PSK, sid, "",
		cmd, shell, int(g.Timeout.Seconds()),
		&stdout, &stderr)
	if err != nil {
		var rerr *RemoteError
		if errors.As(err, &rerr) {
			return stdout.String(), stderr.String(), rerr.ExitCode, nil
		}
		return "", "", 0, err
	}
	return stdout.String(), stderr.String(), rc, nil
}

// requireSuccess returns nil if the cmd ran with rc=0, else a wrapped
// RemoteError carrying stderr context.
func requireSuccess(stdout, stderr string, rc int, what string) error {
	if rc == 0 {
		return nil
	}
	msg := stderr
	if msg == "" {
		msg = stdout
	}
	return &RemoteError{
		error:    fmt.Errorf("%s failed (rc=%d): %s", what, rc, trimNewlines(msg)),
		ExitCode: rc,
	}
}

func trimNewlines(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
