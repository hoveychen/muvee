package auth

import (
	"testing"
	"time"
)

func TestIsAPITokenPrefix(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"mvt_abc123", true},
		{"mvp_abc123", true},
		{"mvt_", true},
		{"mvp_", true},
		{"", false},
		{"mvt", false},
		{"mvp", false},
		{"mv_abc", false},
		{"eyJhbGciOi...", false}, // looks like a JWT
		{"bearer mvp_abc", false},
		{"Mvp_abc", false}, // case-sensitive
	}
	for _, tc := range cases {
		if got := isAPITokenPrefix(tc.in); got != tc.want {
			t.Errorf("isAPITokenPrefix(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestIsTokenExpired(t *testing.T) {
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	if isTokenExpired(nil, now) {
		t.Error("nil expiresAt should mean never expires")
	}
	if !isTokenExpired(&past, now) {
		t.Error("past expiresAt should be expired")
	}
	if isTokenExpired(&future, now) {
		t.Error("future expiresAt should not be expired")
	}
	// Boundary: exactly-equal-to-now counts as expired (strict After).
	equal := now
	if !isTokenExpired(&equal, now) {
		t.Error("expiresAt == now should count as expired")
	}
}
