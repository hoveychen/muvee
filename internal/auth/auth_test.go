package auth

import (
	"context"
	"testing"
	"time"
)

// fakeProvider is a minimal Provider used to drive isOrgScopedProvider tests
// without going through the real OAuth client constructors.
type fakeProvider struct {
	name      string
	orgScoped bool
}

func (p fakeProvider) Name() string              { return p.name }
func (p fakeProvider) DisplayName() string       { return p.name }
func (p fakeProvider) AuthCodeURL(string) string { return "" }
func (p fakeProvider) UserInfo(context.Context, string) (string, string, string, error) {
	return "", "", "", nil
}
func (p fakeProvider) OrgScoped() bool { return p.orgScoped }

func TestIsOrgScopedProvider(t *testing.T) {
	registered := map[string]Provider{
		"google": fakeProvider{name: "google", orgScoped: false},
		"feishu": fakeProvider{name: "feishu", orgScoped: true},
	}

	cases := []struct {
		name     string
		provider string
		want     bool
	}{
		{"registered org-scoped feishu", "feishu", true},
		{"registered non-org-scoped google", "google", false},
		// Fallback path: provider isn't registered locally (e.g. authservice
		// has feishu but muvee-server in some test deployment doesn't). The
		// canonical list still kicks in for known org-scoped providers.
		{"unregistered fallback wecom", "wecom", true},
		{"unregistered fallback dingtalk", "dingtalk", true},
		{"unregistered fallback unknown", "github", false},
		{"empty name", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isOrgScopedProvider(registered, c.provider)
			if got != c.want {
				t.Errorf("isOrgScopedProvider(%q) = %v, want %v", c.provider, got, c.want)
			}
		})
	}
}

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
