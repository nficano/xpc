package sshlife

import (
	"crypto/rand"
	"crypto/rsa"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func mustPubKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa keygen: %v", err)
	}
	pub, err := ssh.NewPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("ssh.NewPublicKey: %v", err)
	}
	return pub
}

func tcpAddr(t *testing.T) net.Addr {
	t.Helper()
	a, _ := net.ResolveTCPAddr("tcp", "127.0.0.1:22")
	return a
}

func TestTOFU_FirstContactWritesEntry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	cb := TOFUHostKey(path)
	key := mustPubKey(t)

	if err := cb("xp-vm:22", tcpAddr(t), key); err != nil {
		t.Fatalf("first contact: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.HasPrefix(string(data), "xp-vm ") {
		t.Fatalf("expected entry to start with hostname; got %q", string(data))
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("known_hosts perm = %o; want 0600", st.Mode().Perm())
	}
}

func TestTOFU_SecondContactWithSameKeySucceeds(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	cb := TOFUHostKey(path)
	key := mustPubKey(t)

	if err := cb("xp-vm:22", tcpAddr(t), key); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := cb("xp-vm:22", tcpAddr(t), key); err != nil {
		t.Fatalf("second: %v", err)
	}
}

func TestTOFU_KeyChangeRejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	cb := TOFUHostKey(path)
	first := mustPubKey(t)
	second := mustPubKey(t)

	if err := cb("xp-vm:22", tcpAddr(t), first); err != nil {
		t.Fatalf("first: %v", err)
	}
	err := cb("xp-vm:22", tcpAddr(t), second)
	if err == nil {
		t.Fatal("expected key-change rejection")
	}
	if !strings.Contains(err.Error(), "host key for xp-vm changed") {
		t.Fatalf("err = %v; want host-key-changed message", err)
	}
}

func TestTOFU_DifferentHostsCoexist(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	cb := TOFUHostKey(path)

	if err := cb("vm-a:22", tcpAddr(t), mustPubKey(t)); err != nil {
		t.Fatalf("first host: %v", err)
	}
	if err := cb("vm-b:22", tcpAddr(t), mustPubKey(t)); err != nil {
		t.Fatalf("second host: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "vm-a ") || !strings.Contains(string(data), "vm-b ") {
		t.Fatalf("expected both host entries; got:\n%s", string(data))
	}
}
