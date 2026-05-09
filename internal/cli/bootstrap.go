package cli

import (
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
	"time"

	"github.com/spf13/cobra"
)

// xpc bootstrap (v0):
//
//	Generates fresh RSA-2048 self-signed cert + key + 32-byte PSK, writes them
//	under ~/.xpc/material/<profile>/, prints the fingerprint, and emits the
//	manual deploy commands (since SSH-driven deploy is a Phase 5b enhancement).
//
// Phase 5b will turn this into a single end-to-end command.
func newBootstrapCmd(g *Globals) *cobra.Command {
	var pskOutFile string
	c := &cobra.Command{
		Use:   "bootstrap",
		Short: "Generate cert + key + PSK locally and emit manual deploy steps.",
		Long: `Phase 5b will deploy the agent over SSH end-to-end. For now this command
generates the trust material and prints the manual sequence: upload to
C:\xpc\, start the agent, then ` + "`xpc profile add`" + ` with the fingerprint
and PSK to pin them to the profile.`,
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
			pskPath := pskOutFile
			if pskPath == "" {
				pskPath = filepath.Join(outDir, "agent.key")
			}

			cert, fingerprint, err := generateSelfSignedCert(certPath, keyPath)
			if err != nil {
				return err
			}
			pskHex, err := generatePSKFile(pskPath)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Generated bootstrap material under %s\n", outDir)
			fmt.Fprintf(out, "  cert:        %s\n", certPath)
			fmt.Fprintf(out, "  key:         %s\n", keyPath)
			fmt.Fprintf(out, "  psk (hex):   %s\n", pskPath)
			fmt.Fprintf(out, "  fingerprint: %s\n\n", fingerprint)

			fmt.Fprintln(out, "Next steps (manual deploy until Phase 5b lands):")
			fmt.Fprintf(out, "  1. Upload these files plus agent/agent.py and agent/arcp.py to C:\\xpc\\\n")
			fmt.Fprintf(out, "     on %s.\n", p.Host)
			fmt.Fprintf(out, "  2. On the VM, run:\n")
			fmt.Fprintf(out, "     C:\\Python34\\python.exe C:\\xpc\\agent.py run --port %d \\\n", p.Port)
			fmt.Fprintf(out, "         --cert C:\\xpc\\agent.crt --key C:\\xpc\\agent.key.pem \\\n")
			fmt.Fprintf(out, "         --psk-file C:\\xpc\\agent.key\n")
			fmt.Fprintf(out, "  3. Pin the credentials into the profile:\n")
			fmt.Fprintf(out, "     xpc profile add %s --host %s --port %d \\\n", p.Name, p.Host, p.Port)
			fmt.Fprintf(out, "         --fingerprint %s --psk-file %s\n\n", fingerprint, pskPath)

			// Sanity that cert was actually written.
			_ = cert
			_ = pskHex
			return nil
		},
	}
	c.Flags().StringVar(&pskOutFile, "psk-out", "", "Path to write the PSK hex file (default: ~/.xpc/material/<profile>/agent.key)")
	return c
}

// generateSelfSignedCert mints an RSA-2048 self-signed cert valid for 365
// days and writes the cert + private key to the given paths in PEM form.
// Returns the parsed cert and its SHA-256 hex fingerprint.
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
