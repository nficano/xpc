package transport

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// generateSelfSignedCert mints an in-memory RSA-2048 self-signed cert valid
// for localhost. Matches what production xpc agents will use (docs/PROTOCOL.md
// §5.2) so the test exercises the same RSA cipher suites the client accepts.
func generateSelfSignedCert(t *testing.T) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "xpc-test"},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		BasicConstraintsValid: true,
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	tlsCert := tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
		Leaf:        cert,
	}
	return tlsCert, cert
}

func TestFingerprintIsLowerHexSHA256(t *testing.T) {
	t.Parallel()
	_, cert := generateSelfSignedCert(t)
	got := Fingerprint(cert)
	if len(got) != 64 {
		t.Fatalf("fingerprint length = %d; want 64", len(got))
	}
	if strings.ToLower(got) != got {
		t.Fatalf("fingerprint not lowercase: %q", got)
	}
}

func TestNormalizeFingerprint(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"AB:CD:EF":        "abcdef",
		"sha256:AB:CD:EF": "abcdef",
		"AbCdEf":          "abcdef",
		" sha256:abcdef ": "abcdef",
		"abcdef":          "abcdef",
	}
	for in, want := range cases {
		got := NormalizeFingerprint(in)
		if got != want {
			t.Fatalf("NormalizeFingerprint(%q) = %q; want %q", in, got, want)
		}
	}
}

// TestDialWithFingerprintPinning spins up an in-process TLS 1.2 server with
// a self-signed cert and verifies the client accepts the matching
// fingerprint and rejects a wrong one.
func TestDialWithFingerprintPinning(t *testing.T) {
	t.Parallel()
	tlsCert, cert := generateSelfSignedCert(t)
	fp := Fingerprint(cert)

	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		MinVersion:   tls.VersionTLS12,
		MaxVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatalf("tls.Listen: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			// tls.Listen's Accept returns a *tls.Conn whose handshake is
			// lazy. Explicitly run it so the client side sees a successful
			// completion (or a fingerprint check) instead of EOF.
			if tc, ok := conn.(*tls.Conn); ok {
				_ = tc.SetDeadline(time.Now().Add(2 * time.Second))
				_ = tc.Handshake()
			}
			_ = conn.Close()
		}
	}()

	// Single cleanup: close the listener (causing Accept to error out and
	// the goroutine to return), then wait for the goroutine to finish.
	// Combining into one cleanup avoids the LIFO ordering pitfall where
	// wg.Wait would otherwise run before the close and deadlock.
	t.Cleanup(func() {
		_ = listener.Close()
		wg.Wait()
	})

	addr := listener.Addr().String()

	t.Run("accepts matching fingerprint", func(t *testing.T) {
		conn, err := Dial(addr, fp, 2*time.Second)
		if err != nil {
			t.Fatalf("Dial: %v", err)
		}
		_ = conn.Close()
	})

	t.Run("rejects wrong fingerprint", func(t *testing.T) {
		wrong := strings.Repeat("a", 64)
		_, err := Dial(addr, wrong, 2*time.Second)
		if err == nil {
			t.Fatal("expected error on fingerprint mismatch; got nil")
		}
		if !strings.Contains(err.Error(), "fingerprint mismatch") {
			t.Fatalf("err = %v; want fingerprint mismatch", err)
		}
	})

	t.Run("rejects wrong fingerprint format", func(t *testing.T) {
		// Fingerprint with sha256: prefix and colons should still work.
		formatted := "sha256:" + strings.Join(splitN(fp, 2), ":")
		conn, err := Dial(addr, formatted, 2*time.Second)
		if err != nil {
			t.Fatalf("Dial with formatted fingerprint: %v", err)
		}
		_ = conn.Close()
	})
}

// splitN returns s broken into chunks of n characters each.
func splitN(s string, n int) []string {
	if n <= 0 {
		return []string{s}
	}
	out := make([]string, 0, (len(s)+n-1)/n)
	for i := 0; i < len(s); i += n {
		end := i + n
		if end > len(s) {
			end = len(s)
		}
		out = append(out, s[i:end])
	}
	return out
}
