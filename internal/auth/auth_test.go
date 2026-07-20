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

func (p fakeProvider) Name() string                      { return p.name }
func (p fakeProvider) DisplayName() string               { return p.name }
func (p fakeProvider) AuthCodeURL(_, _ string) string    { return "" }
func (p fakeProvider) CanonicalRedirectURL() string      { return "" }
func (p fakeProvider) UserInfo(context.Context, string, string) (string, string, string, error) {
	return "", "", "", nil
}
func (p fakeProvider) OrgScoped() bool { return p.orgScoped }

// fakeSubjectProvider embeds fakeProvider and also implements SubjectProvider.
type fakeSubjectProvider struct {
	fakeProvider
	sub       string
	email     string
	name      string
	avatarURL string
}

func (p fakeSubjectProvider) UserInfoWithSubject(context.Context, string, string, string) (string, string, string, string, error) {
	return p.sub, p.email, p.name, p.avatarURL, nil
}

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

// TestSubjectProviderOptional verifies that the SubjectProvider optional
// interface contract holds: a plain Provider must NOT accidentally satisfy
// SubjectProvider, and an implementor must return its subject through the
// type-asserted call. The auth callback handler relies on this assertion to
// decide between the email-keyed path (UpsertUser) and the (provider, sub)
// path (EnsureUserByOAuth).
func TestSubjectProviderOptional(t *testing.T) {
	var plain Provider = fakeProvider{name: "legacy", orgScoped: false}
	if _, ok := plain.(SubjectProvider); ok {
		t.Errorf("plain Provider must NOT satisfy SubjectProvider")
	}
	var social Provider = fakeSubjectProvider{
		fakeProvider: fakeProvider{name: "discord"},
		sub:          "987654321",
		email:        "",
		name:         "alice",
		avatarURL:    "https://cdn.discord/x.png",
	}
	sp, ok := social.(SubjectProvider)
	if !ok {
		t.Fatalf("fakeSubjectProvider must satisfy SubjectProvider")
	}
	gotSub, gotEmail, gotName, gotAvatar, err := sp.UserInfoWithSubject(context.Background(), "code", "state", "")
	if err != nil {
		t.Fatalf("UserInfoWithSubject error: %v", err)
	}
	if gotSub != "987654321" {
		t.Errorf("sub = %q, want %q", gotSub, "987654321")
	}
	if gotEmail != "" {
		t.Errorf("email should stay empty for IdP that did not surface one, got %q", gotEmail)
	}
	if gotName != "alice" {
		t.Errorf("name = %q, want %q", gotName, "alice")
	}
	if gotAvatar == "" {
		t.Errorf("avatar should propagate through, got empty")
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
