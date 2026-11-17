package main

import (
	"sort"
	"testing"

	"github.com/hoveychen/muvee/internal/api"
)

// TestFwdCookieNamesMatchStripList guards against drift between the cookies
// authservice may set (fwdAllCookieNames) and the explicit stripCookies list
// the HTTP provider hands to every embed-bridge instance
// (api.FwdAuthCookieNames). A name missing from the latter would reach
// tenants (BACKLOG-20261117-007 §6).
func TestFwdCookieNamesMatchStripList(t *testing.T) {
	got := append([]string(nil), fwdAllCookieNames()...)
	want := append([]string(nil), api.FwdAuthCookieNames...)
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("authservice cookies %v != strip list %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("authservice cookies %v != strip list %v", got, want)
		}
	}
	seen := map[string]bool{}
	for _, n := range want {
		if seen[n] {
			t.Errorf("strip list has duplicate %q", n)
		}
		seen[n] = true
	}
	for _, n := range []string{"__Host-muvee_fwd", "__Host-muvee_sso", "muvee_fwd_session", "muvee_invite_token"} {
		if !seen[n] {
			t.Errorf("strip list is missing %q", n)
		}
	}
}
