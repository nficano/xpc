// Package sshlife provides a thin SSH client used by xpc bootstrap and the
// agent-lifecycle subcommands. We deliberately use a minimal feature set:
//
//	Dial(addr, user, password)
//	Run(cmd) (stdout, stderr, exitStatus, err)
//	PutFile(localPath, remotePath) -- via `cat > <remote>` over stdin pipe
//	PutBytes(data, remotePath)     -- same, from an in-memory buffer
//
// The remote shell is the Cygwin bash sshd that xpctl bootstraps on the VM.
// Paths are POSIX-style (/cygdrive/c/...) for the upload helpers; the
// PutFile/PutBytes wrappers convert from C:\... automatically.
//
// Host-key trust uses TOFU (trust on first use): the first connection to a
// new host writes its key to ~/.xpc/known_hosts; subsequent connections
// require a byte-for-byte match. A changed key short-circuits the dial with
// an error.
package sshlife

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// Client is a thin wrapper around an *ssh.Client that opens a fresh session
// for each Run/PutFile/PutBytes call.
type Client struct {
	c    *ssh.Client
	addr string
}

// DialOptions configures Dial.
type DialOptions struct {
	User     string
	Password string
	Timeout  time.Duration
	// HostKeyCallback overrides the default TOFU callback. Leave nil to
	// trust on first use against ~/.xpc/known_hosts.
	HostKeyCallback ssh.HostKeyCallback
	// KnownHostsPath overrides the default ~/.xpc/known_hosts location.
	KnownHostsPath string
}

// Dial opens an SSH connection. addr is "host:port"; if no port, 22 is used.
func Dial(addr string, opt DialOptions) (*Client, error) {
	if !strings.Contains(addr, ":") {
		addr = addr + ":22"
	}
	if opt.Timeout == 0 {
		opt.Timeout = 10 * time.Second
	}
	hk := opt.HostKeyCallback
	if hk == nil {
		path := opt.KnownHostsPath
		if path == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("ssh: home dir: %w", err)
			}
			path = filepath.Join(home, ".xpc", "known_hosts")
		}
		hk = TOFUHostKey(path)
	}
	cfg := &ssh.ClientConfig{
		User:            opt.User,
		Auth:            []ssh.AuthMethod{ssh.Password(opt.Password)},
		HostKeyCallback: hk,
		Timeout:         opt.Timeout,
		HostKeyAlgorithms: []string{
			"ssh-rsa",
			"rsa-sha2-256",
			"rsa-sha2-512",
			"ssh-ed25519",
			"ecdsa-sha2-nistp256",
		},
	}
	conn, err := net.DialTimeout("tcp", addr, opt.Timeout)
	if err != nil {
		return nil, fmt.Errorf("ssh: dial %s: %w", addr, err)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ssh: handshake %s: %w", addr, err)
	}
	return &Client{c: ssh.NewClient(sshConn, chans, reqs), addr: addr}, nil
}

// Close releases the SSH connection.
func (c *Client) Close() error {
	if c == nil || c.c == nil {
		return nil
	}
	err := c.c.Close()
	c.c = nil
	return err
}

// Run executes cmd in a remote shell, returning combined stdout, stderr, and
// the exit status. A non-zero exit status is returned in the int but does
// NOT produce an error; callers decide whether to treat it as failure.
func (c *Client) Run(cmd string, timeout time.Duration) (stdout, stderr string, exitStatus int, err error) {
	if c == nil || c.c == nil {
		return "", "", 0, errors.New("ssh: nil client")
	}
	sess, err := c.c.NewSession()
	if err != nil {
		return "", "", 0, fmt.Errorf("ssh: session: %w", err)
	}
	defer func() { _ = sess.Close() }()

	var outBuf, errBuf bytes.Buffer
	sess.Stdout = &outBuf
	sess.Stderr = &errBuf

	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	select {
	case err := <-done:
		exitStatus = exitStatusFromErr(err)
		return outBuf.String(), errBuf.String(), exitStatus, nil
	case <-time.After(timeout):
		_ = sess.Signal(ssh.SIGTERM)
		return outBuf.String(), errBuf.String(), -1, fmt.Errorf("ssh: command timed out after %s: %s", timeout, cmd)
	}
}

func exitStatusFromErr(err error) int {
	if err == nil {
		return 0
	}
	var ee *ssh.ExitError
	if errors.As(err, &ee) {
		return ee.ExitStatus()
	}
	return 1
}

// PutFile uploads a local file to remotePath. remotePath is a Windows-style
// path (e.g. C:\xpc\agent.py); it's translated to /cygdrive/c/xpc/agent.py
// for the bash `cat > ...` invocation.
func (c *Client) PutFile(localPath, remotePath string, timeout time.Duration) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("ssh: open %s: %w", localPath, err)
	}
	defer func() { _ = f.Close() }()
	return c.putFromReader(f, remotePath, timeout)
}

// PutBytes is PutFile from an in-memory byte slice.
func (c *Client) PutBytes(data []byte, remotePath string, timeout time.Duration) error {
	return c.putFromReader(bytes.NewReader(data), remotePath, timeout)
}

func (c *Client) putFromReader(r io.Reader, remotePath string, timeout time.Duration) error {
	if c == nil || c.c == nil {
		return errors.New("ssh: nil client")
	}
	cyg := winToCygwin(remotePath)
	dir := cygDir(cyg)

	// Ensure parent dir exists, then `cat > <path>` from stdin.
	if dir != "" {
		if _, _, _, err := c.Run("mkdir -p "+shellQ(dir), timeout); err != nil {
			return fmt.Errorf("ssh: mkdir %s: %w", dir, err)
		}
	}

	sess, err := c.c.NewSession()
	if err != nil {
		return fmt.Errorf("ssh: session: %w", err)
	}
	defer func() { _ = sess.Close() }()

	stdin, err := sess.StdinPipe()
	if err != nil {
		return fmt.Errorf("ssh: stdin pipe: %w", err)
	}
	if err := sess.Start("cat > " + shellQ(cyg)); err != nil {
		return fmt.Errorf("ssh: start cat: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(stdin, r)
		_ = stdin.Close()
		done <- copyErr
	}()

	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	select {
	case copyErr := <-done:
		if copyErr != nil {
			_ = sess.Signal(ssh.SIGTERM)
			return fmt.Errorf("ssh: copy bytes to %s: %w", cyg, copyErr)
		}
	case <-time.After(timeout):
		_ = sess.Signal(ssh.SIGTERM)
		return fmt.Errorf("ssh: PutFile timed out after %s for %s", timeout, cyg)
	}

	if err := sess.Wait(); err != nil {
		return fmt.Errorf("ssh: cat > %s exited with error: %w", cyg, err)
	}
	return nil
}

// winToCygwin converts "C:\xpc\foo" -> "/cygdrive/c/xpc/foo".
func winToCygwin(p string) string {
	if len(p) >= 2 && p[1] == ':' {
		drive := strings.ToLower(p[:1])
		rest := strings.ReplaceAll(p[2:], "\\", "/")
		if !strings.HasPrefix(rest, "/") {
			rest = "/" + rest
		}
		return "/cygdrive/" + drive + rest
	}
	return strings.ReplaceAll(p, "\\", "/")
}

func cygDir(p string) string {
	idx := strings.LastIndex(p, "/")
	if idx <= 0 {
		return ""
	}
	return p[:idx]
}

func shellQ(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// TOFUHostKey returns an ssh.HostKeyCallback that:
//
//   - accepts and records the host key on first contact (writes to path)
//   - rejects subsequent connections whose key differs from the recorded one
//
// The known_hosts file uses the standard OpenSSH-ish line format
// "<host> <key-type> <base64-key>". Multiple entries per host are tolerated;
// the callback succeeds if any line matches the presented key exactly.
func TOFUHostKey(path string) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		encoded := base64.StdEncoding.EncodeToString(key.Marshal())
		canonical := canonicalHostName(hostname, remote)

		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("ssh: read %s: %w", path, err)
		}

		// Look for an existing entry for this host.
		var existingTypes []string
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.SplitN(line, " ", 3)
			if len(parts) < 3 {
				continue
			}
			if !hostMatches(parts[0], canonical) {
				continue
			}
			if parts[1] == key.Type() && parts[2] == encoded {
				return nil
			}
			existingTypes = append(existingTypes, parts[1])
		}

		if len(existingTypes) > 0 {
			return fmt.Errorf(
				"ssh: host key for %s changed (have %s in %s, presenting %s) -- potential MITM, refusing",
				canonical, strings.Join(existingTypes, ","), path, key.Type())
		}

		// First contact: append.
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("ssh: mkdir %s: %w", filepath.Dir(path), err)
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("ssh: open %s: %w", path, err)
		}
		defer func() { _ = f.Close() }()
		if _, err := fmt.Fprintf(f, "%s %s %s\n", canonical, key.Type(), encoded); err != nil {
			return fmt.Errorf("ssh: append %s: %w", path, err)
		}
		return nil
	}
}

// canonicalHostName strips the :port suffix that x/crypto/ssh adds to
// hostname, matching the OpenSSH known_hosts convention.
func canonicalHostName(hostname string, _ net.Addr) string {
	if idx := strings.LastIndex(hostname, ":"); idx > 0 {
		// Make sure it isn't an IPv6 literal "[::1]:22".
		if !strings.Contains(hostname[:idx], "]") || strings.HasPrefix(hostname, "[") {
			return hostname[:idx]
		}
	}
	return hostname
}

// hostMatches checks whether a known_hosts hostname field matches the dialed
// hostname. We do not implement OpenSSH's full hashed/wildcard semantics --
// just exact match plus a few common formats.
func hostMatches(stored, dialed string) bool {
	if stored == dialed {
		return true
	}
	// Stored "h1,h2" comma list.
	for _, h := range strings.Split(stored, ",") {
		if h == dialed {
			return true
		}
	}
	return false
}
