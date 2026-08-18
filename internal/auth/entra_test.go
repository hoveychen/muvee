package auth

import (
	"net/url"
	"strings"
	"testing"
)

const testTenantGUID = "72f988bf-86f1-41af-91ab-2d7cd011db47"

func TestNewEntraProviderIncompleteConfigIsNotConfigured(t *testing.T) {
	cases := []struct {
		name string
		cfg  EntraConfig
	}{
		{"all empty", EntraConfig{}},
		{"no tenant", EntraConfig{ClientID: "cid", ClientSecret: "sec"}},
		{"no client id", EntraConfig{TenantID: testTenantGUID, ClientSecret: "sec"}},
		{"no secret", EntraConfig{TenantID: testTenantGUID, ClientID: "cid"}},
		{"whitespace only", EntraConfig{TenantID: " ", ClientID: " ", ClientSecret: " "}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := newEntraProvider(c.cfg, "https://example.com/auth/entra/callback")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p != nil {
				t.Fatalf("expected nil provider for incomplete config, got %+v", p)
			}
		})
	}
}

func TestNewEntraProviderRejectsMalformedTenant(t *testing.T) {
	for _, tenant := range []string{"../evil", "tenant/extra", "a b", "tenant?x=1", "-leading"} {
		t.Run(tenant, func(t *testing.T) {
			p, err := newEntraProvider(EntraConfig{TenantID: tenant, ClientID: "cid", ClientSecret: "sec"}, "https://example.com/cb")
			if err == nil {
				t.Fatalf("expected error for tenant %q, got provider %+v", tenant, p)
			}
		})
	}
}

func TestNewEntraProviderEndpoints(t *testing.T) {
	p, err := newEntraProvider(EntraConfig{TenantID: testTenantGUID, ClientID: "cid", ClientSecret: "sec"},
		"https://muvee.example/auth/entra/callback")
	if err != nil || p == nil {
		t.Fatalf("newEntraProvider: p=%v err=%v", p, err)
	}
	authority := "https://login.microsoftonline.com/" + testTenantGUID
	if got, want := p.config.Endpoint.AuthURL, authority+"/oauth2/v2.0/authorize"; got != want {
		t.Errorf("AuthURL = %q, want %q", got, want)
	}
	if got, want := p.config.Endpoint.TokenURL, authority+"/oauth2/v2.0/token"; got != want {
		t.Errorf("TokenURL = %q, want %q", got, want)
	}
	if got, want := entraIssuer(testTenantGUID), authority+"/v2.0"; got != want {
		t.Errorf("issuer = %q, want %q", got, want)
	}
	if got := strings.Join(p.config.Scopes, " "); got != "openid profile email" {
		t.Errorf("scopes = %q, want %q", got, "openid profile email")
	}
	if p.Name() != "entra" || p.DisplayName() != "Microsoft" {
		t.Errorf("Name/DisplayName = %q/%q", p.Name(), p.DisplayName())
	}
	if got, want := p.CanonicalRedirectURL(), "https://muvee.example/auth/entra/callback"; got != want {
		t.Errorf("CanonicalRedirectURL = %q, want %q", got, want)
	}
}

// The multi-domain platform path rebases the callback host per request and
// passes the result to AuthCodeURL; the baked config must not be mutated.
func TestEntraAuthCodeURLRedirectOverride(t *testing.T) {
	p, err := newEntraProvider(EntraConfig{TenantID: "contoso.onmicrosoft.com", ClientID: "cid", ClientSecret: "sec"},
		"https://muvee.example/auth/entra/callback")
	if err != nil || p == nil {
		t.Fatalf("newEntraProvider: p=%v err=%v", p, err)
	}
	raw := p.AuthCodeURL("state-123", "https://alt.example/auth/entra/callback")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse auth url: %v", err)
	}
	q := u.Query()
	if got, want := q.Get("redirect_uri"), "https://alt.example/auth/entra/callback"; got != want {
		t.Errorf("redirect_uri = %q, want %q", got, want)
	}
	if got := q.Get("state"); got != "state-123" {
		t.Errorf("state = %q", got)
	}
	if got := q.Get("client_id"); got != "cid" {
		t.Errorf("client_id = %q", got)
	}
	if !strings.HasPrefix(raw, "https://login.microsoftonline.com/contoso.onmicrosoft.com/oauth2/v2.0/authorize") {
		t.Errorf("authorize endpoint wrong: %q", raw)
	}
	if p.config.RedirectURL != "https://muvee.example/auth/entra/callback" {
		t.Errorf("baked RedirectURL mutated: %q", p.config.RedirectURL)
	}
}

func TestEntraOrgScoped(t *testing.T) {
	cases := map[string]bool{
		testTenantGUID:            true,  // single tenant: directory membership is authorisation
		"contoso.onmicrosoft.com": true,  // verified domain: still one directory
		"common":                  false, // any directory or personal account
		"organizations":           false,
		"consumers":               false,
	}
	for tenant, want := range cases {
		t.Run(tenant, func(t *testing.T) {
			p, err := newEntraProvider(EntraConfig{TenantID: tenant, ClientID: "cid", ClientSecret: "sec"}, "https://example.com/cb")
			if err != nil || p == nil {
				t.Fatalf("newEntraProvider(%q): p=%v err=%v", tenant, p, err)
			}
			if got := p.OrgScoped(); got != want {
				t.Errorf("OrgScoped() = %v, want %v", got, want)
			}
		})
	}
}

func TestValidateEntraIssuer(t *testing.T) {
	otherGUID := "00000000-0000-0000-0000-000000000001"
	cases := []struct {
		name      string
		tenant    string
		issuer    string
		tid       string
		wantError bool
	}{
		{
			name:   "guid tenant, matching token",
			tenant: testTenantGUID,
			issuer: "https://login.microsoftonline.com/" + testTenantGUID + "/v2.0",
			tid:    testTenantGUID,
		},
		{
			name:      "guid tenant, token from another tenant",
			tenant:    testTenantGUID,
			issuer:    "https://login.microsoftonline.com/" + otherGUID + "/v2.0",
			tid:       otherGUID,
			wantError: true,
		},
		{
			name:   "domain tenant resolves to a guid issuer",
			tenant: "contoso.onmicrosoft.com",
			issuer: "https://login.microsoftonline.com/" + testTenantGUID + "/v2.0",
			tid:    testTenantGUID,
		},
		{
			name:   "multi-tenant alias accepts any well-formed issuer",
			tenant: "organizations",
			issuer: "https://login.microsoftonline.com/" + otherGUID + "/v2.0",
			tid:    otherGUID,
		},
		{
			name:      "issuer does not match tid",
			tenant:    "common",
			issuer:    "https://login.microsoftonline.com/" + testTenantGUID + "/v2.0",
			tid:       otherGUID,
			wantError: true,
		},
		{
			name:      "foreign issuer host",
			tenant:    "common",
			issuer:    "https://evil.example/" + testTenantGUID + "/v2.0",
			tid:       testTenantGUID,
			wantError: true,
		},
		{
			name:      "missing tid",
			tenant:    testTenantGUID,
			issuer:    "https://login.microsoftonline.com/" + testTenantGUID + "/v2.0",
			tid:       "",
			wantError: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateEntraIssuer(c.tenant, c.issuer, c.tid)
			if c.wantError && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !c.wantError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestEntraSubject(t *testing.T) {
	cases := []struct {
		name    string
		claims  entraClaims
		subject string
		want    string
	}{
		{
			name:    "oid namespaced by tid",
			claims:  entraClaims{TenantID: "t1", ObjectID: "o1"},
			subject: "pairwise",
			want:    "t1:o1",
		},
		{
			name:    "oid without tid",
			claims:  entraClaims{ObjectID: "o1"},
			subject: "pairwise",
			want:    "o1",
		},
		{
			name:    "falls back to sub",
			claims:  entraClaims{TenantID: "t1"},
			subject: "pairwise",
			want:    "pairwise",
		},
		{
			name: "no identifiers at all",
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := entraSubject(c.claims, c.subject); got != c.want {
				t.Errorf("entraSubject() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestEntraEmail(t *testing.T) {
	cases := []struct {
		name   string
		claims entraClaims
		want   string
	}{
		{"email claim wins", entraClaims{Email: "a@corp.com", PreferredUsername: "b@corp.com"}, "a@corp.com"},
		{"falls back to upn", entraClaims{PreferredUsername: "b@corp.com"}, "b@corp.com"},
		{"non-email upn ignored", entraClaims{PreferredUsername: "someuser"}, ""},
		{"nothing", entraClaims{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := entraEmail(c.claims); got != c.want {
				t.Errorf("entraEmail() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestEntraProviderImplementsSubjectProvider(t *testing.T) {
	p, err := newEntraProvider(EntraConfig{TenantID: testTenantGUID, ClientID: "cid", ClientSecret: "sec"}, "https://example.com/cb")
	if err != nil || p == nil {
		t.Fatalf("newEntraProvider: p=%v err=%v", p, err)
	}
	var _ Provider = p
	if _, ok := any(p).(SubjectProvider); !ok {
		t.Fatal("entraProvider does not implement SubjectProvider")
	}
}
