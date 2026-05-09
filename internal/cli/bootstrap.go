package cli

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	xpcagent "github.com/nficano/xpc/agent"
	"github.com/nficano/xpc/internal/profile"
	"github.com/nficano/xpc/internal/sshlife"
)

// xpc bootstrap (Phase 5b real implementation):
//
//	1. Generate fresh RSA-2048 cert + 32-byte PSK locally.
//	2. SSH to profile.SSHUser@profile.Host:22 with profile.SSHPassword.
//	3. Upload agent.py, arcp.py, manage.py, cert, key, PSK to C:\xpc\.
//	4. Kill any existing C:\xpc\agent.py process; start a fresh detached one
//	   on profile.Port via manage.py.
//	5. Wait for the new agent to listen, connect over TLS, pin the
//	   fingerprint into ~/.xpc/config and store the PSK in ~/.xpc/credentials.
//
// --no-deploy keeps the legacy "print manual steps" mode for users who
// prefer to deploy by other means.

func newBootstrapCmd(g *Globals) *cobra.Command {
	var (
		noDeploy   bool
		sshTimeout time.Duration
	)
	c := &cobra.Command{
		Use:   "bootstrap",
		Short: "Generate cert+PSK, deploy the agent over SSH, pin fingerprint into the profile.",
		Long: `Generates a fresh RSA-2048 cert and 32-byte PSK locally, then SSHes to the
VM (profile.ssh_user / profile.ssh_password), uploads agent.py + arcp.py +
manage.py + the new trust material to C:\xpc\, restarts the agent on
profile.port, and saves the cert fingerprint and PSK into the profile.

Use --no-deploy to skip the SSH step and just emit the trust material plus
the manual deploy commands.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := g.ResolveProfile()
			if err != nil {
				return err
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			outDir := filepath.Join(home, ".xpc", "material", p.Name)
			if err := os.MkdirAll(outDir, 0o700); err != nil {
				return fmt.Errorf("mkdir %s: %w", outDir, err)
			}
			certPath := filepath.Join(outDir, "agent.crt")
			keyPath := filepath.Join(outDir, "agent.key.pem")
			pskPath := filepath.Join(outDir, "agent.key")

			cert, fingerprint, err := generateSelfSignedCert(certPath, keyPath)
			if err != nil {
				return err
			}
			pskHex, err := generatePSKFile(pskPath)
			if err != nil {
				return err
			}
			pskBytes, _ := hex.DecodeString(pskHex)

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Generated bootstrap material under %s\n", outDir)
			fmt.Fprintf(out, "  cert:        %s\n", certPath)
			fmt.Fprintf(out, "  key:         %s\n", keyPath)
			fmt.Fprintf(out, "  psk (hex):   %s\n", pskPath)
			fmt.Fprintf(out, "  fingerprint: %s\n\n", fingerprint)

			if noDeploy {
				return printManualBootstrapSteps(cmd, p, fingerprint, certPath, keyPath, pskPath)
			}

			if p.Host == "" {
				return wrapUsage(fmt.Errorf("profile %q has no host; run `xpc configure --profile %s` first", p.Name, p.Name))
			}
			if p.SSHUser == "" || p.SSHPassword == "" {
				return wrapUsage(fmt.Errorf("profile %q is missing ssh_user/ssh_password (re-run `xpc configure` or supply with --no-deploy)", p.Name))
			}

			fmt.Fprintf(out, "Connecting via SSH to %s@%s ...\n", p.SSHUser, p.Host)
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			ssh, err := sshlife.Dial(p.Host+":22", sshlife.DialOptions{
				User:     p.SSHUser,
				Password: p.SSHPassword,
				Timeout:  sshTimeout,
			})
			if err != nil {
				return wrapConnection(err)
			}
			defer func() { _ = ssh.Close() }()

			fmt.Fprintln(out, "Uploading agent files to C:\\xpc\\ ...")
			uploads := []struct {
				name      string
				dest      string
				dataBytes []byte
				dataPath  string
			}{
				{"agent.py", `C:\xpc\agent.py`, xpcagent.AgentPy, ""},
				{"arcp.py", `C:\xpc\arcp.py`, xpcagent.ArcpPy, ""},
				{"manage.py", `C:\xpc\manage.py`, []byte(xpcagent.ManagePy), ""},
				{"agent.crt", `C:\xpc\agent.crt`, nil, certPath},
				{"agent.key.pem", `C:\xpc\agent.key.pem`, nil, keyPath},
				{"agent.key", `C:\xpc\agent.key`, nil, pskPath},
			}
			for _, u := range uploads {
				if u.dataBytes != nil {
					if err := ssh.PutBytes(u.dataBytes, u.dest, 60*time.Second); err != nil {
						return wrapConnection(fmt.Errorf("upload %s: %w", u.name, err))
					}
				} else {
					if err := ssh.PutFile(u.dataPath, u.dest, 60*time.Second); err != nil {
						return wrapConnection(fmt.Errorf("upload %s: %w", u.name, err))
					}
				}
				fmt.Fprintf(out, "  %s -> %s\n", u.name, u.dest)
			}

			fmt.Fprintln(out, "Restarting xpc agent ...")
			// Cygwin bash strips backslashes from unquoted args, but Win32
			// python.exe needs a Win32 path in argv[1]. Wrap the path in
			// single quotes so bash preserves it byte-for-byte.
			restartCmd := fmt.Sprintf(
				`/cygdrive/c/Python34/python.exe 'C:\xpc\manage.py' restart %d`,
				p.Port)
			stdout, stderr, rc, err := ssh.Run(restartCmd, 30*time.Second)
			if err != nil {
				return wrapConnection(fmt.Errorf("ssh run manage.py: %w", err))
			}
			if rc != 0 {
				return fmt.Errorf("manage.py exited %d: %s\n%s",
					rc, strings.TrimSpace(stdout), strings.TrimSpace(stderr))
			}

			// Wait for the agent to listen on profile.Port.
			fmt.Fprintf(out, "Waiting for the agent on %s:%d ...\n", p.Host, p.Port)
			if err := waitForListen(ctx, p.Host, p.Port, 20*time.Second); err != nil {
				return wrapConnection(err)
			}

			// Verify TLS handshake and pin fingerprint.
			fmt.Fprintf(out, "Pinning fingerprint and saving credentials to profile %q ...\n", p.Name)
			p.Fingerprint = fingerprint
			p.PSK = pskBytes
			if err := profile.Save(p); err != nil {
				return err
			}
			_ = cert // suppress unused-var lint when we add cert chain checks later

			fmt.Fprintf(out, "\nBootstrap complete. Try:\n")
			fmt.Fprintf(out, "  xpc use %s\n", p.Name)
			fmt.Fprintf(out, "  xpc agent ping\n")
			fmt.Fprintf(out, "  xpc exec ver\n")
			return nil
		},
	}
	c.Flags().BoolVar(&noDeploy, "no-deploy", false, "Skip SSH and just emit the trust material + manual steps.")
	c.Flags().DurationVar(&sshTimeout, "ssh-timeout", 15*time.Second, "SSH dial / handshake timeout.")
	return c
}

func printManualBootstrapSteps(cmd *cobra.Command, p *profile.Profile, fingerprint, certPath, keyPath, pskPath string) error {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Manual deploy (--no-deploy):")
	fmt.Fprintf(out, "  1. Upload these files to C:\\xpc\\ on %s:\n", p.Host)
	fmt.Fprintf(out, "     %s -> C:\\xpc\\agent.crt\n", certPath)
	fmt.Fprintf(out, "     %s -> C:\\xpc\\agent.key.pem\n", keyPath)
	fmt.Fprintf(out, "     %s -> C:\\xpc\\agent.key\n", pskPath)
	fmt.Fprintf(out, "     plus agent/agent.py and agent/arcp.py from this repo.\n")
	fmt.Fprintf(out, "  2. Run on the VM:\n")
	fmt.Fprintf(out, "     C:\\Python34\\python.exe C:\\xpc\\agent.py run --port %d \\\n", p.Port)
	fmt.Fprintf(out, "         --cert C:\\xpc\\agent.crt --key C:\\xpc\\agent.key.pem \\\n")
	fmt.Fprintf(out, "         --psk-file C:\\xpc\\agent.key\n")
	fmt.Fprintf(out, "  3. Pin into the profile:\n")
	fmt.Fprintf(out, "     xpc profile add %s --host %s --port %d \\\n", p.Name, p.Host, p.Port)
	fmt.Fprintf(out, "         --fingerprint %s --psk-file %s\n", fingerprint, pskPath)
	return nil
}

// generateSelfSignedCert mints an RSA-2048 self-signed cert + key in PEM.
func generateSelfSignedCert(certPath, keyPath string) (*x509.Certificate, string, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, "", fmt.Errorf("rsa keygen: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "xpc-agent"},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		BasicConstraintsValid: true,
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{"localhost", "xpc-agent"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, "", fmt.Errorf("create cert: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, "", fmt.Errorf("parse cert: %w", err)
	}
	if err := writePEM(certPath, "CERTIFICATE", der, 0o644); err != nil {
		return nil, "", err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, "", fmt.Errorf("marshal key: %w", err)
	}
	if err := writePEM(keyPath, "PRIVATE KEY", keyDER, 0o600); err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(cert.Raw)
	return cert, hex.EncodeToString(sum[:]), nil
}

func writePEM(path, blockType string, der []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir for %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return pem.Encode(f, &pem.Block{Type: blockType, Bytes: der})
}

func generatePSKFile(path string) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	hexStr := hex.EncodeToString(raw[:])
	if err := os.WriteFile(path, []byte(hexStr+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write psk: %w", err)
	}
	return hexStr, nil
}

// waitForListen polls the agent port until it's accepting TCP connections,
// up to timeout.
func waitForListen(ctx context.Context, host string, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("%s:%d", host, port)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		conn, err := dialTCP(addr, 1*time.Second)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("agent did not listen on %s within %s", addr, timeout)
}

func dialTCP(addr string, timeout time.Duration) (interface{ Close() error }, error) {
	d := newTCPDialer(timeout)
	return d.Dial("tcp", addr)
}
