package auth

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// EntraConfig holds a Microsoft Entra ID (formerly Azure AD) app registration.
// Loaded from system_settings (`entra_*`) so admins can configure it at runtime
// via /admin/settings, with ENTRA_* env vars as a fallback. RedirectURL is NOT
// here — each plane computes its own callback URL:
//
//	platform : https://<base-domain>/auth/entra/callback
//	project  : <FORWARD_AUTH_BASE_URL>/_oauth/entra   (BuildSocialProviders)
//
// Both must be registered on the same Azure app registration ("Web" platform)
// when Entra login is enabled on both planes.
type EntraConfig struct {
	// TenantID is the Azure tenant: a directory GUID, a verified domain
	// (contoso.onmicrosoft.com), or one of the multi-tenant aliases
	// "common" / "organizations" / "consumers". A GUID is the strictest
	// option — UserInfo then requires the ID token's `tid` claim to match it
	// exactly, so tokens minted by any other tenant are rejected.
	TenantID     string
	ClientID     string
	ClientSecret string
}

// entraMultiTenantAliases are the Microsoft-reserved tenant values that let
// users from *any* directory (or, for "consumers", personal Microsoft accounts)
// sign in. With one of these the tenant no longer restricts who can
// authenticate, so the provider reports OrgScoped() = false and the platform
// keeps enforcing ALLOWED_DOMAINS on the returned email.
var entraMultiTenantAliases = map[string]bool{
	"common":        true,
	"organizations": true,
	"consumers":     true,
}

// entraTenantPattern guards the tenant value before it is interpolated into
// the authority URL: GUIDs, DNS-style domains and the aliases only. Without
// this a tenant like "../evil" would rewrite the authorization endpoint path.
var entraTenantPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9.\-]*[a-z0-9])?$`)

type entraProvider struct {
	config   *oauth2.Config
	tenant   string
	verifier *gooidc.IDTokenVerifier
}

// entraAuthority returns the tenant-scoped v2.0 authority base. Endpoints under
// it are deterministic, so the provider is constructed without an OIDC
// discovery round-trip — construction stays offline-safe (it runs at
// muvee-server boot and on every authservice provider reload) and the JWKS is
// fetched lazily by the verifier on the first token validation.
func entraAuthority(tenant string) string {
	return "https://login.microsoftonline.com/" + tenant
}

func entraIssuer(tenant string) string {
	return entraAuthority(tenant) + "/v2.0"
}

// newEntraProvider builds the provider from an explicit config. Returns
// (nil, nil) when any required field is empty so callers can treat that as
// "not configured" and skip registration, matching the other providers.
func newEntraProvider(cfg EntraConfig, redirectURL string) (*entraProvider, error) {
	tenant := strings.ToLower(strings.TrimSpace(cfg.TenantID))
	clientID := strings.TrimSpace(cfg.ClientID)
	clientSecret := strings.TrimSpace(cfg.ClientSecret)
	if tenant == "" || clientID == "" || clientSecret == "" {
		return nil, nil
	}
	if !entraTenantPattern.MatchString(tenant) {
		return nil, fmt.Errorf("invalid tenant %q: expected a directory GUID, a verified domain, or common/organizations/consumers", cfg.TenantID)
	}
	keySet := gooidc.NewRemoteKeySet(context.Background(), entraAuthority(tenant)+"/discovery/v2.0/keys")
	return &entraProvider{
		tenant: tenant,
		config: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Endpoint: oauth2.Endpoint{
				AuthURL:  entraAuthority(tenant) + "/oauth2/v2.0/authorize",
				TokenURL: entraAuthority(tenant) + "/oauth2/v2.0/token",
			},
			Scopes: []string{gooidc.ScopeOpenID, "profile", "email"},
		},
		// SkipIssuerCheck because the issuer is only known per token: for a
		// GUID tenant it is entraIssuer(tenant), but for a domain tenant or a
		// multi-tenant alias Microsoft mints `https://login.microsoftonline.com/
		// <tid>/v2.0` with the *resolved* directory GUID. validateEntraIssuer
		// re-imposes the check on the claims instead — the audience check
		// (ClientID) is still done by the verifier.
		verifier: gooidc.NewVerifier(entraIssuer(tenant), keySet, &gooidc.Config{
			ClientID:        clientID,
			SkipIssuerCheck: true,
		}),
	}, nil
}

func (p *entraProvider) Name() string                 { return "entra" }
func (p *entraProvider) DisplayName() string          { return "Microsoft" }
func (p *entraProvider) CanonicalRedirectURL() string { return p.config.RedirectURL }

// OrgScoped is true for a single-tenant configuration: only members (and
// guests) of that Entra directory can complete the flow at all, which is the
// same "membership is authorisation" property Feishu / WeCom / DingTalk have,
// so the email-domain whitelist is skipped. Multi-tenant aliases admit any
// directory, so they stay subject to ALLOWED_DOMAINS.
func (p *entraProvider) OrgScoped() bool { return !entraMultiTenantAliases[p.tenant] }

func (p *entraProvider) cfgFor(redirectURL string) *oauth2.Config {
	if redirectURL == "" {
		return p.config
	}
	c := *p.config
	c.RedirectURL = redirectURL
	return &c
}

func (p *entraProvider) AuthCodeURL(state, redirectURL string) string {
	return p.cfgFor(redirectURL).AuthCodeURL(state, oauth2.AccessTypeOnline)
}

// UserInfo satisfies Provider for the platform-side flow, which keys identity
// on the email address.
func (p *entraProvider) UserInfo(ctx context.Context, code, redirectURL string) (email, name, avatarURL string, err error) {
	_, email, name, avatarURL, err = p.UserInfoWithSubject(ctx, code, "", redirectURL)
	return
}

// UserInfoWithSubject additionally returns a stable subject, so identity can be
// keyed on (provider, sub) via oauth_accounts for an account that exposes no
// email claim. Note that authservice's callback currently only calls UserInfo —
// the SubjectProvider path is not wired into it for any provider yet — so this
// exists for parity with the social providers rather than as a live code path.
func (p *entraProvider) UserInfoWithSubject(ctx context.Context, code, _, redirectURL string) (sub, email, name, avatarURL string, err error) {
	token, err := p.cfgFor(redirectURL).Exchange(ctx, code)
	if err != nil {
		return "", "", "", "", fmt.Errorf("exchange code: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return "", "", "", "", fmt.Errorf("no id_token")
	}
	idToken, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return "", "", "", "", fmt.Errorf("verify token: %w", err)
	}
	var claims entraClaims
	if err := idToken.Claims(&claims); err != nil {
		return "", "", "", "", fmt.Errorf("parse claims: %w", err)
	}
	if err := validateEntraIssuer(p.tenant, idToken.Issuer, claims.TenantID); err != nil {
		return "", "", "", "", err
	}
	sub = entraSubject(claims, idToken.Subject)
	if sub == "" {
		return "", "", "", "", fmt.Errorf("id_token has neither oid nor sub")
	}
	return sub, entraEmail(claims), claims.Name, "", nil
}

// entraClaims are the ID-token claims muvee reads. Entra v2.0 has no `picture`
// claim — a profile photo would need a separate Microsoft Graph call — so the
// avatar stays empty.
type entraClaims struct {
	TenantID          string `json:"tid"`
	ObjectID          string `json:"oid"`
	Email             string `json:"email"`
	PreferredUsername string `json:"preferred_username"`
	Name              string `json:"name"`
}

// validateEntraIssuer re-imposes the issuer check the verifier skipped. Two
// invariants: the issuer must be exactly the authority of the tenant that
// minted the token (so a token from an unrelated IdP can never pass), and, when
// the configured tenant is a GUID, that tenant must be the minting one. Domain
// tenants and multi-tenant aliases can only be checked structurally — the tid
// they resolve to is not known from configuration alone.
func validateEntraIssuer(configuredTenant, issuer, tid string) error {
	tid = strings.ToLower(strings.TrimSpace(tid))
	if tid == "" {
		return fmt.Errorf("id_token has no tid claim")
	}
	if want := entraIssuer(tid); !strings.EqualFold(strings.TrimSpace(issuer), want) {
		return fmt.Errorf("id_token issuer %q does not match its tenant (%s)", issuer, want)
	}
	if isEntraGUID(configuredTenant) && !strings.EqualFold(configuredTenant, tid) {
		return fmt.Errorf("id_token was issued by tenant %s, not the configured %s", tid, configuredTenant)
	}
	return nil
}

var entraGUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func isEntraGUID(s string) bool {
	return entraGUIDPattern.MatchString(strings.ToLower(strings.TrimSpace(s)))
}

// entraSubject returns the stable identity key for oauth_accounts. `oid` is the
// directory object id — it survives UPN/email changes and app-registration
// churn — and is scoped to a tenant, so it is namespaced by `tid` to stay
// unique if the deployment is (or later becomes) multi-tenant. Falls back to
// the pairwise `sub` claim when `oid` is absent.
func entraSubject(claims entraClaims, subject string) string {
	if oid := strings.TrimSpace(claims.ObjectID); oid != "" {
		if tid := strings.TrimSpace(claims.TenantID); tid != "" {
			return tid + ":" + oid
		}
		return oid
	}
	return strings.TrimSpace(subject)
}

// entraEmail prefers the `email` claim and falls back to `preferred_username`,
// which for work/school accounts carries the UPN. Guest and some federated
// accounts surface neither, in which case downstream identity binds on the
// subject instead and the platform path rejects the login (no email to check
// against ALLOWED_DOMAINS).
func entraEmail(claims entraClaims) string {
	if e := strings.TrimSpace(claims.Email); e != "" {
		return e
	}
	if u := strings.TrimSpace(claims.PreferredUsername); strings.Contains(u, "@") {
		return u
	}
	return ""
}
