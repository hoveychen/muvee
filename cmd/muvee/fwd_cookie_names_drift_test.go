package main

import (
	"os"
	"regexp"
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

// TestPluginDefaultStripListCoversAllCookies: the embed-bridge plugin's built-in
// default (a separate Go module, so read from source) must list every cookie
// authservice may set. Instances without an explicit list — embed-bridge@file
// in a host-mounted dynamic.yml, and the routers an older muvee-server
// generates after an image rollback — strip only the default, and a missing
// name would reach tenants.
func TestPluginDefaultStripListCoversAllCookies(t *testing.T) {
	src, err := os.ReadFile("../../traefik/plugins-local/src/github.com/hoveychen/embed-bridge/embed_bridge.go")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)var defaultStripCookies = \[\]string\{(.*?)\n\}`).FindSubmatch(src)
	if m == nil {
		t.Fatal("defaultStripCookies not found in the embed-bridge plugin source")
	}
	listed := map[string]bool{}
	for _, q := range regexp.MustCompile(`"([^"]+)"`).FindAllSubmatch(m[1], -1) {
		listed[string(q[1])] = true
	}
	for _, n := range fwdAllCookieNames() {
		if !listed[n] {
			t.Errorf("plugin default strip list is missing %q", n)
		}
	}
}
