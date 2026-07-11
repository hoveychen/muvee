package api

import (
	"strings"
	"testing"
)

func TestValidateDemoUsername(t *testing.T) {
	valid := []string{"demo", "a", "demo-user", "demo.user_1", "0abc"}
	for _, u := range valid {
		if err := validateDemoUsername(u); err != nil {
			t.Errorf("validateDemoUsername(%q) = %v, want nil", u, err)
		}
	}
	invalid := []string{
		"",              // required
		"Demo",          // uppercase
		"-demo",         // leading punctuation
		"demo-",         // trailing punctuation
		"de mo",         // whitespace
		"demo@site",     // illegal char
		strings.Repeat("a", 65), // too long
	}
	for _, u := range invalid {
		if err := validateDemoUsername(u); err == nil {
			t.Errorf("validateDemoUsername(%q) = nil, want error", u)
		}
	}
	if err := validateDemoUsername(strings.Repeat("a", 64)); err != nil {
		t.Errorf("64-char username should be valid, got %v", err)
	}
}

func TestValidateDemoPassword(t *testing.T) {
	if err := validateDemoPassword("short"); err == nil {
		t.Error("short password should be rejected")
	}
	if err := validateDemoPassword(strings.Repeat("x", 73)); err == nil {
		t.Error(">72-byte password should be rejected (bcrypt input limit)")
	}
	for _, p := range []string{"password", strings.Repeat("x", 72)} {
		if err := validateDemoPassword(p); err != nil {
			t.Errorf("validateDemoPassword(%q) = %v, want nil", p, err)
		}
	}
}

func TestNormalizeDemoUsername(t *testing.T) {
	if got := normalizeDemoUsername("  Demo.User "); got != "demo.user" {
		t.Errorf("normalizeDemoUsername = %q, want %q", got, "demo.user")
	}
}
