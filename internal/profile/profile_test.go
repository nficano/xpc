package profile

import (
	"os"
	"path/filepath"
	"testing"
)

func setHomeForTest(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XPC_HOST", "")
	t.Setenv("XPC_PORT", "")
	t.Setenv("XPC_FINGERPRINT", "")
	t.Setenv("XPC_SSH_USER", "")
	t.Setenv("XPC_SSH_PASSWORD", "")
	t.Setenv("XPC_PSK", "")
	return tmp
}

func TestLoadEmptyReturnsDefaults(t *testing.T) {
	setHomeForTest(t)
	p, err := Load(DefaultName)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.Name != DefaultName {
		t.Fatalf("name = %q; want %q", p.Name, DefaultName)
	}
	if p.Port != defaultPort {
		t.Fatalf("port = %d; want %d", p.Port, defaultPort)
	}
	if !p.VerifyHostKey {
		t.Fatal("verify_host_key default = true; got false")
	}
	if p.Host != "" {
		t.Fatalf("expected empty host; got %q", p.Host)
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	home := setHomeForTest(t)
	want := &Profile{
		Name:          "lab",
		Host:          "xp-truvoice-w02",
		Port:          9578,
		Fingerprint:   "abcd1234",
		SSHUser:       "DONALD TRUMP",
		SSHPassword:   "mywinxp!",
		PSK:           []byte("0123456789abcdef0123456789abcdef"),
		ProxmoxHost:   "pve.example",
		ProxmoxUser:   "root@pam",
		VerifyHostKey: false,
	}
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// 0o700 dir, 0o600 files.
	st, err := os.Stat(filepath.Join(home, dirName))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("dir perm = %o; want 0700", st.Mode().Perm())
	}
	for _, name := range []string{configFile, credentialsFile} {
		st, err := os.Stat(filepath.Join(home, dirName, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("%s perm = %o; want 0600", name, st.Mode().Perm())
		}
	}

	got, err := Load("lab")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Host != want.Host || got.Port != want.Port ||
		got.Fingerprint != want.Fingerprint ||
		got.SSHUser != want.SSHUser ||
		got.SSHPassword != want.SSHPassword ||
		got.ProxmoxHost != want.ProxmoxHost ||
		got.ProxmoxUser != want.ProxmoxUser ||
		got.VerifyHostKey != want.VerifyHostKey ||
		string(got.PSK) != string(want.PSK) {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestListDeleteAndActive(t *testing.T) {
	setHomeForTest(t)

	for _, name := range []string{"lab", "stage", "prod"} {
		if err := Save(&Profile{Name: name, Host: "h", Port: 9578, VerifyHostKey: true}); err != nil {
			t.Fatalf("Save %s: %v", name, err)
		}
	}

	got, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"lab", "prod", "stage"}
	if len(got) != len(want) {
		t.Fatalf("List len = %d; want %d (%v)", len(got), len(want), got)
	}
	for i, n := range want {
		if got[i] != n {
			t.Fatalf("List[%d] = %q; want %q", i, got[i], n)
		}
	}

	if err := Delete("stage"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	after, _ := List()
	if len(after) != 2 || after[1] != "prod" {
		t.Fatalf("after delete = %v", after)
	}

	// Active default fallback.
	if name, _ := Active(); name != DefaultName {
		t.Fatalf("Active default = %q; want %q", name, DefaultName)
	}
	if err := SetActive("lab"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if name, _ := Active(); name != "lab" {
		t.Fatalf("Active after set = %q; want %q", name, "lab")
	}
}

func TestEnvOverridesWin(t *testing.T) {
	setHomeForTest(t)
	if err := Save(&Profile{Name: "x", Host: "h-from-file", Port: 9999, VerifyHostKey: true}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	t.Setenv("XPC_HOST", "h-from-env")
	t.Setenv("XPC_PORT", "8888")
	p, err := Load("x")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.Host != "h-from-env" {
		t.Fatalf("Host = %q; want env override", p.Host)
	}
	if p.Port != 8888 {
		t.Fatalf("Port = %d; want 8888", p.Port)
	}
}

func TestLoadMissingProfileReturnsDefaults(t *testing.T) {
	setHomeForTest(t)
	p, err := Load("does-not-exist")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.Host != "" || p.Port != defaultPort {
		t.Fatalf("expected empty defaults; got %+v", p)
	}
}
