package main

import (
	"strings"
	"testing"
	"time"
)

func setTestSecret(t *testing.T) {
	t.Helper()
	jwtSecret = []byte("test-secret-32-byte-min-pad-pad-pad-pad")
}

func TestSignStateRoundTrip(t *testing.T) {
	setTestSecret(t)

	in := stateClaims{Mode: "login-token", LoginToken: "tok-abc"}
	signed, err := signState(in)
	if err != nil {
		t.Fatalf("signState: %v", err)
	}
	if !strings.Contains(signed, ".") {
		t.Fatalf("expected payload.signature form, got %q", signed)
	}

	out, err := verifyState(signed)
	if err != nil {
		t.Fatalf("verifyState: %v", err)
	}
	if out.Mode != "login-token" || out.LoginToken != "tok-abc" {
		t.Fatalf("claims round-trip mismatch: %+v", out)
	}
	if out.Nonce == "" {
		t.Fatalf("signState should have filled Nonce automatically")
	}
	if out.IssuedAt == 0 {
		t.Fatalf("signState should have filled IssuedAt automatically")
	}
}

func TestVerifyState_RejectsTamperedSignature(t *testing.T) {
	setTestSecret(t)

	signed, err := signState(stateClaims{Mode: "login-token", LoginToken: "x"})
	if err != nil {
		t.Fatalf("signState: %v", err)
	}
	// Flip the last char of the signature — invalidate the HMAC.
	bad := signed[:len(signed)-1] + "A"
	if signed[len(signed)-1] == 'A' {
		bad = signed[:len(signed)-1] + "B"
	}
	if _, err := verifyState(bad); err == nil {
		t.Fatalf("expected verify to reject tampered signature")
	}
}

func TestVerifyState_RejectsExpiredState(t *testing.T) {
	setTestSecret(t)

	signed, err := signState(stateClaims{
		Mode:     "login-token",
		Nonce:    "fixed-nonce",
		IssuedAt: time.Now().Add(-time.Duration(stateMaxAge+60) * time.Second).Unix(),
	})
	if err != nil {
		t.Fatalf("signState: %v", err)
	}
	if _, err := verifyState(signed); err == nil {
		t.Fatalf("expected verify to reject expired state")
	}
}

func TestVerifyState_RejectsLegacyStateFormat(t *testing.T) {
	setTestSecret(t)
	// Legacy random-int state (no dot, no HMAC) — used by the cookie-anchored
	// path. verifyState must reject so handleOAuthCallback falls through to
	// the legacy cookie-compare branch instead of accidentally treating these
	// as login-token flows.
	if _, err := verifyState("1234567890"); err == nil {
		t.Fatalf("expected verify to reject malformed state")
	}
	if _, err := verifyState("device-1234567890"); err == nil {
		t.Fatalf("expected verify to reject legacy device- prefix")
	}
}
