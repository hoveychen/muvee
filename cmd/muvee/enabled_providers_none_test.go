package main

import "testing"

// TestProjectEnabledFwdProviders_None verifies the "none" sentinel yields zero
// OAuth providers (password/SMS-only sign-in), regardless of what the process
// has loaded. It returns before touching the global provider set, so no setup
// is needed.
func TestProjectEnabledFwdProviders_None(t *testing.T) {
	for _, in := range []string{"none", "NONE", " none "} {
		if got := projectEnabledFwdProviders(in); len(got) != 0 {
			t.Errorf("projectEnabledFwdProviders(%q) = %d providers, want 0", in, len(got))
		}
	}
}
