package arcp

import (
	"bytes"
	"strings"
	"testing"
)

var testPSK = bytes.Repeat([]byte{0}, 32)

func TestSignAndVerifyRoundTrip(t *testing.T) {
	t.Parallel()
	e := newSampleEnvelope()
	if err := Sign(e, testPSK); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if e.Auth.Sig == "" {
		t.Fatal("Sign did not populate auth.sig")
	}
	if err := VerifySig(e, testPSK); err != nil {
		t.Fatalf("VerifySig: %v", err)
	}
}

func TestVerifyRejectsTamperedEnvelope(t *testing.T) {
	t.Parallel()
	e := newSampleEnvelope()
	if err := Sign(e, testPSK); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	e.ID = "msg_TAMPERED"
	err := VerifySig(e, testPSK)
	if err == nil || !strings.Contains(err.Error(), "signature mismatch") {
		t.Fatalf("expected signature mismatch; got %v", err)
	}
}

func TestVerifyRejectsTamperedSig(t *testing.T) {
	t.Parallel()
	e := newSampleEnvelope()
	if err := Sign(e, testPSK); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	e.Auth.Sig = "00000000000000000000000000000000000000000000000000000000000000aa"
	err := VerifySig(e, testPSK)
	if err == nil || !strings.Contains(err.Error(), "signature mismatch") {
		t.Fatalf("expected mismatch; got %v", err)
	}
}

func TestVerifyRejectsWrongPSK(t *testing.T) {
	t.Parallel()
	e := newSampleEnvelope()
	if err := Sign(e, testPSK); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	other := bytes.Repeat([]byte{1}, 32)
	if err := VerifySig(e, other); err == nil {
		t.Fatal("expected mismatch with wrong PSK")
	}
}

func TestSignRequiresPSK(t *testing.T) {
	t.Parallel()
	e := newSampleEnvelope()
	if err := Sign(e, nil); err == nil {
		t.Fatal("expected error on empty PSK")
	}
}

func TestVerifyRejectsBadHexSig(t *testing.T) {
	t.Parallel()
	e := newSampleEnvelope()
	e.Auth.Sig = "not-hex"
	if err := VerifySig(e, testPSK); err == nil {
		t.Fatal("expected error on bad hex sig")
	}
}

func TestVerifyRejectsEmptySig(t *testing.T) {
	t.Parallel()
	e := newSampleEnvelope()
	if err := VerifySig(e, testPSK); err == nil {
		t.Fatal("expected error on empty sig")
	}
}

// TestSignDeterministic verifies that signing the same envelope twice
// produces the same signature (HMAC has no randomness).
func TestSignDeterministic(t *testing.T) {
	t.Parallel()
	e1 := newSampleEnvelope()
	e2 := newSampleEnvelope()
	if err := Sign(e1, testPSK); err != nil {
		t.Fatalf("Sign 1: %v", err)
	}
	if err := Sign(e2, testPSK); err != nil {
		t.Fatalf("Sign 2: %v", err)
	}
	if e1.Auth.Sig != e2.Auth.Sig {
		t.Fatalf("non-deterministic Sign:\n%q\n%q", e1.Auth.Sig, e2.Auth.Sig)
	}
}
