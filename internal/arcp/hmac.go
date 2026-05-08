package arcp

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
)

// Sign computes HMAC-SHA256 over the envelope's canonical signing input and
// stores the lowercase-hex digest in e.Auth.Sig.
func Sign(e *Envelope, psk []byte) error {
	if len(psk) == 0 {
		return fmt.Errorf("arcp: sign: empty psk")
	}
	canon, err := canonicalSigningInput(e)
	if err != nil {
		return err
	}
	mac := hmac.New(sha256.New, psk)
	mac.Write(canon)
	e.Auth.Sig = hex.EncodeToString(mac.Sum(nil))
	return nil
}

// VerifySig recomputes the envelope's HMAC-SHA256 and constant-time compares
// it against e.Auth.Sig. It does not change the envelope.
func VerifySig(e *Envelope, psk []byte) error {
	if len(psk) == 0 {
		return fmt.Errorf("arcp: verify: empty psk")
	}
	if e == nil {
		return fmt.Errorf("arcp: verify: nil envelope")
	}
	if e.Auth.Alg != AuthAlg {
		return fmt.Errorf("arcp: verify: unsupported alg %q", e.Auth.Alg)
	}
	if e.Auth.Kid != AuthKID {
		return fmt.Errorf("arcp: verify: unsupported kid %q", e.Auth.Kid)
	}
	if e.Auth.Sig == "" {
		return fmt.Errorf("arcp: verify: empty signature")
	}

	got, err := hex.DecodeString(e.Auth.Sig)
	if err != nil {
		return fmt.Errorf("arcp: verify: invalid hex sig: %w", err)
	}

	canon, err := canonicalSigningInput(e)
	if err != nil {
		return err
	}
	mac := hmac.New(sha256.New, psk)
	mac.Write(canon)
	expected := mac.Sum(nil)

	if subtle.ConstantTimeCompare(expected, got) != 1 {
		return fmt.Errorf("arcp: verify: signature mismatch")
	}
	return nil
}
