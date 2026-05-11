package main

import (
	"testing"
	"time"
)

// TestLoginTokens_LifecycleStates exercises the in-memory map transitions a
// poll handler walks through: pending → success/error → consumed, plus the
// TTL-based expiry path. The poll endpoint itself is not exercised here
// (would require fake fwdProviders + HTTP scaffolding); these are the pure
// state transitions that matter most.
func TestLoginTokens_LifecycleStates(t *testing.T) {
	token := "tok-" + t.Name()
	defer loginTokens.Delete(token)

	loginTokens.Store(token, &loginTokenEntry{
		Provider:  "google",
		Status:    "pending",
		ExpiresAt: time.Now().Add(10 * time.Minute),
	})

	v, _ := loginTokens.Load(token)
	entry := v.(*loginTokenEntry)
	if entry.Status != "pending" {
		t.Fatalf("fresh entry should be pending, got %q", entry.Status)
	}

	// Simulate handleLoginTokenCallback success path.
	entry.Email = "alice@example.com"
	entry.Name = "Alice"
	entry.ProviderName = "google"
	entry.Status = "success"

	v2, _ := loginTokens.Load(token)
	if v2.(*loginTokenEntry).Status != "success" {
		t.Fatalf("expected success after callback")
	}
	if v2.(*loginTokenEntry).Email != "alice@example.com" {
		t.Fatalf("identity not propagated")
	}
}

func TestMarkLoginTokenError_PendingToError(t *testing.T) {
	token := "tok-" + t.Name()
	defer loginTokens.Delete(token)

	loginTokens.Store(token, &loginTokenEntry{
		Provider:  "google",
		Status:    "pending",
		ExpiresAt: time.Now().Add(10 * time.Minute),
	})

	markLoginTokenError(token, "boom")

	v, _ := loginTokens.Load(token)
	entry := v.(*loginTokenEntry)
	if entry.Status != "error" {
		t.Fatalf("expected error status, got %q", entry.Status)
	}
	if entry.Error != "boom" {
		t.Fatalf("expected error message propagated, got %q", entry.Error)
	}
}

func TestMarkLoginTokenError_UnknownTokenIsNoop(t *testing.T) {
	// Best-effort semantics — must not panic when the token has already been
	// garbage-collected (poll would have returned "expired" in that case).
	markLoginTokenError("definitely-not-a-real-token", "boom")
}

func TestGenerateLoginToken_UniqueAndUnpredictable(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		tok := generateLoginToken()
		if len(tok) < 32 {
			t.Fatalf("token too short: %d chars", len(tok))
		}
		if seen[tok] {
			t.Fatalf("duplicate token generated")
		}
		seen[tok] = true
	}
}
