// Package profile implements the AWS-style split-config storage for xpc.
//
// Layout under ~/.xpc/:
//
//	config        non-secret per-profile fields (host, port, fingerprint, ...)
//	credentials   secret per-profile fields (PSK, SSH password)
//	state         single line: active profile name
//
// All reads merge: file value -> XPC_* env var -> CLI flag override (the last
// wins). Writes go to file only; env vars and flags are runtime overrides.
package profile

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/ini.v1"
)

// Profile holds the merged view of a saved profile plus runtime overrides.
type Profile struct {
	Name          string
	Host          string
	Port          int
	Fingerprint   string // sha256 hex of the agent's TLS cert (DER)
	SSHUser       string
	SSHPassword   string
	PSK           []byte // 32 bytes, or nil if not yet provisioned
	ProxmoxHost   string
	ProxmoxUser   string
	VerifyHostKey bool
}

const (
	dirName         = ".xpc"
	configFile      = "config"
	credentialsFile = "credentials"
	stateFile       = "state"
	defaultPort     = 9578
)

// DefaultName is the fallback active profile.
const DefaultName = "default"

// Dir returns the absolute path of the xpc config dir for the calling user.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("profile: home dir: %w", err)
	}
	return filepath.Join(home, dirName), nil
}

// ConfigPath returns the absolute path to ~/.xpc/config.
func ConfigPath() (string, error) { return joinDir(configFile) }

// CredentialsPath returns the absolute path to ~/.xpc/credentials.
func CredentialsPath() (string, error) { return joinDir(credentialsFile) }

// StatePath returns the absolute path to ~/.xpc/state.
func StatePath() (string, error) { return joinDir(stateFile) }

func joinDir(name string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// EnsureDir creates ~/.xpc with mode 0700 if missing.
func EnsureDir() error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("profile: mkdir %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("profile: chmod %s: %w", dir, err)
	}
	return nil
}

// Load reads the profile named name from ~/.xpc/config and ~/.xpc/credentials,
// then applies env-var overrides. CLI flags layer on top via the caller (see
// internal/cli/root.go).
func Load(name string) (*Profile, error) {
	if name == "" {
		name = DefaultName
	}
	p := &Profile{Name: name, Port: defaultPort, VerifyHostKey: true}

	cfgPath, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	if err := readSection(cfgPath, sectionName(name), func(sec *ini.Section) {
		if v := sec.Key("host").String(); v != "" {
			p.Host = v
		}
		if v := sec.Key("port").String(); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				p.Port = n
			}
		}
		if v := sec.Key("fingerprint").String(); v != "" {
			p.Fingerprint = v
		}
		if v := sec.Key("ssh_user").String(); v != "" {
			p.SSHUser = v
		}
		if v := sec.Key("verify_host_key").String(); v != "" {
			p.VerifyHostKey = v != "false"
		}
		if v := sec.Key("proxmox_host").String(); v != "" {
			p.ProxmoxHost = v
		}
		if v := sec.Key("proxmox_user").String(); v != "" {
			p.ProxmoxUser = v
		}
	}); err != nil {
		return nil, err
	}

	credPath, err := CredentialsPath()
	if err != nil {
		return nil, err
	}
	if err := readSection(credPath, sectionName(name), func(sec *ini.Section) {
		if v := sec.Key("psk").String(); v != "" {
			if raw, err := base64.StdEncoding.DecodeString(v); err == nil {
				p.PSK = raw
			} else if raw, err := hex.DecodeString(v); err == nil {
				p.PSK = raw
			}
		}
		if v := sec.Key("ssh_password").String(); v != "" {
			p.SSHPassword = v
		}
	}); err != nil {
		return nil, err
	}

	applyEnvOverrides(p)
	return p, nil
}

// Save writes the profile to ~/.xpc/config and ~/.xpc/credentials. Creates
// the dir + files with 0o700/0o600 perms if missing.
func Save(p *Profile) error {
	if p == nil || p.Name == "" {
		return errors.New("profile: nil or unnamed profile")
	}
	if err := EnsureDir(); err != nil {
		return err
	}

	cfgPath, _ := ConfigPath()
	credPath, _ := CredentialsPath()

	cfg, err := loadOrEmpty(cfgPath)
	if err != nil {
		return err
	}
	sec, _ := cfg.NewSection(sectionName(p.Name))
	if existing, _ := cfg.GetSection(sectionName(p.Name)); existing != nil {
		sec = existing
	}
	sec.Key("host").SetValue(p.Host)
	sec.Key("port").SetValue(strconv.Itoa(p.Port))
	sec.Key("fingerprint").SetValue(p.Fingerprint)
	sec.Key("ssh_user").SetValue(p.SSHUser)
	sec.Key("verify_host_key").SetValue(strconv.FormatBool(p.VerifyHostKey))
	sec.Key("proxmox_host").SetValue(p.ProxmoxHost)
	sec.Key("proxmox_user").SetValue(p.ProxmoxUser)

	if err := writeINI(cfgPath, cfg, 0o600); err != nil {
		return err
	}

	cred, err := loadOrEmpty(credPath)
	if err != nil {
		return err
	}
	csec, _ := cred.NewSection(sectionName(p.Name))
	if existing, _ := cred.GetSection(sectionName(p.Name)); existing != nil {
		csec = existing
	}
	if p.PSK != nil {
		csec.Key("psk").SetValue(base64.StdEncoding.EncodeToString(p.PSK))
	}
	if p.SSHPassword != "" {
		csec.Key("ssh_password").SetValue(p.SSHPassword)
	}
	return writeINI(credPath, cred, 0o600)
}

// Delete removes the named profile from both config and credentials.
func Delete(name string) error {
	for _, path := range []func() (string, error){ConfigPath, CredentialsPath} {
		p, err := path()
		if err != nil {
			return err
		}
		f, err := loadOrEmpty(p)
		if err != nil {
			return err
		}
		f.DeleteSection(sectionName(name))
		if err := writeINI(p, f, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// List returns the sorted names of all profiles found in the config file.
func List() ([]string, error) {
	cfgPath, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	f, err := loadOrEmpty(cfgPath)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, sec := range f.Sections() {
		name := strings.TrimPrefix(sec.Name(), "profile ")
		if name == ini.DefaultSection || name == "" {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// Active reads ~/.xpc/state and returns the active profile name. Falls back
// to DefaultName when state is missing or empty.
func Active() (string, error) {
	p, err := StatePath()
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultName, nil
		}
		return "", err
	}
	name := strings.TrimSpace(string(raw))
	if name == "" {
		return DefaultName, nil
	}
	return name, nil
}

// SetActive writes ~/.xpc/state with the given profile name.
func SetActive(name string) error {
	if err := EnsureDir(); err != nil {
		return err
	}
	p, err := StatePath()
	if err != nil {
		return err
	}
	if err := os.WriteFile(p, []byte(name+"\n"), 0o600); err != nil {
		return fmt.Errorf("profile: write state: %w", err)
	}
	return nil
}

// ---- internals ------------------------------------------------------------

func sectionName(name string) string { return "profile " + name }

func readSection(path, sectionName string, apply func(*ini.Section)) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	f, err := ini.LoadSources(ini.LoadOptions{Loose: true}, path)
	if err != nil {
		return fmt.Errorf("profile: load %s: %w", path, err)
	}
	sec, err := f.GetSection(sectionName)
	if err != nil {
		return nil // missing section is fine
	}
	apply(sec)
	return nil
}

func loadOrEmpty(path string) (*ini.File, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return ini.Empty(), nil
	}
	return ini.LoadSources(ini.LoadOptions{Loose: true}, path)
}

func writeINI(path string, f *ini.File, mode os.FileMode) error {
	if err := EnsureDir(); err != nil {
		return err
	}
	if err := f.SaveTo(path); err != nil {
		return fmt.Errorf("profile: save %s: %w", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("profile: chmod %s: %w", path, err)
	}
	return nil
}

func applyEnvOverrides(p *Profile) {
	if v := os.Getenv("XPC_HOST"); v != "" {
		p.Host = v
	}
	if v := os.Getenv("XPC_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			p.Port = n
		}
	}
	if v := os.Getenv("XPC_FINGERPRINT"); v != "" {
		p.Fingerprint = v
	}
	if v := os.Getenv("XPC_SSH_USER"); v != "" {
		p.SSHUser = v
	}
	if v := os.Getenv("XPC_SSH_PASSWORD"); v != "" {
		p.SSHPassword = v
	}
	if v := os.Getenv("XPC_PSK"); v != "" {
		// Accept hex or base64.
		if raw, err := hex.DecodeString(v); err == nil {
			p.PSK = raw
		} else if raw, err := base64.StdEncoding.DecodeString(v); err == nil {
			p.PSK = raw
		}
	}
}
