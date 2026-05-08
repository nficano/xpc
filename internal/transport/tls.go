// Package transport implements the wire transport beneath the ARCP envelope:
// TLS 1.2 over TCP, length-prefixed framing, and self-signed certificate
// pinning by SHA-256 fingerprint. See docs/PROTOCOL.md §5.
package transport

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// DefaultPort is the well-known port for xpc agents.
const DefaultPort = 9578

// DefaultTimeout is the per-attempt connect timeout.
const DefaultTimeout = 10 * time.Second

// Fingerprint returns the lowercase-hex SHA-256 of a certificate's DER bytes.
// This is the canonical "fingerprint" used in profiles and TOFU prompts.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

// NormalizeFingerprint accepts the human-friendly forms ("sha256:AB:CD:..."
// or just hex) and returns the lowercase-hex form Fingerprint produces.
func NormalizeFingerprint(s string) string {
	s = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "sha256:")
	// Strip optional colon separators (e.g. "AB:CD:..." style).
	return strings.ReplaceAll(s, ":", "")
}

// PinFingerprint returns a tls.VerifyConnection callback that succeeds iff
// the peer's leaf certificate's SHA-256 fingerprint equals expectedHex.
//
// Hostname verification is NOT performed; the fingerprint is the trust
// anchor. The returned callback is safe to share across goroutines.
func PinFingerprint(expectedHex string) func(tls.ConnectionState) error {
	want := NormalizeFingerprint(expectedHex)
	return func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return errors.New("transport: peer presented no certificates")
		}
		got := Fingerprint(cs.PeerCertificates[0])
		if got != want {
			return fmt.Errorf("transport: cert fingerprint mismatch: got %s want %s", got, want)
		}
		return nil
	}
}

// ClientConfig builds the tls.Config used by xpc clients to dial agents.
//
// InsecureSkipVerify is set to true intentionally: standard verification is
// skipped because we anchor trust on the fingerprint pin via
// VerifyConnection. This pattern is recommended by Go's crypto/tls
// documentation for self-signed-with-pinning workflows.
func ClientConfig(expectedFingerprintHex string) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		MaxVersion: tls.VersionTLS12,
		// Cipher suites confirmed working on the live VM's Python 3.4
		// OpenSSL 1.0.2k (see docs/INVESTIGATION.md). Go's stdlib does not
		// include AES_256_CBC_SHA384, so the GCM variants do the heavy
		// lifting.
		CipherSuites: []uint16{
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256,
		},
		InsecureSkipVerify: true, //nolint:gosec // pinned via VerifyConnection per docs/PROTOCOL.md §5.2
		VerifyConnection:   PinFingerprint(expectedFingerprintHex),
	}
}

// Dial connects to addr (host:port) and performs a TLS 1.2 handshake,
// rejecting any peer whose leaf cert's SHA-256 fingerprint differs from
// expectedFingerprintHex.
func Dial(addr, expectedFingerprintHex string, timeout time.Duration) (net.Conn, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	cfg := ClientConfig(expectedFingerprintHex)
	d := net.Dialer{Timeout: timeout}
	return tls.DialWithDialer(&d, "tcp", addr, cfg)
}
