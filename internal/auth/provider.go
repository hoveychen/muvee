package auth

import "context"

// Provider handles the OAuth2 flow for a specific identity provider.
type Provider interface {
	// Name returns the provider identifier used in URLs (e.g. "google", "feishu").
	Name() string
	// DisplayName returns a human-readable name for the provider.
	DisplayName() string
	// AuthCodeURL returns the authorization URL to redirect the user to.
	AuthCodeURL(state string) string
	// UserInfo exchanges the authorization code for the user's email, name, and avatar URL.
	UserInfo(ctx context.Context, code string) (email, name, avatarURL string, err error)
	// OrgScoped returns true if the provider inherently restricts users to a specific
	// organisation (e.g. Feishu, WeCom, DingTalk). For such providers the email domain
	// check is skipped because membership in the org is sufficient authorisation.
	OrgScoped() bool
}

// SubjectProvider is an optional capability implemented by providers whose
// IdP exposes a stable per-user subject identifier alongside (or instead of)
// an email address. Social providers (Discord / Apple / Facebook / Twitter)
// implement this because their IdPs may not surface an email at all, so the
// downstream auth path keys identity on (provider, sub) via the
// oauth_accounts table instead of users.email.
//
// Platform-side providers (Google / Feishu / WeCom / DingTalk) currently do
// NOT implement this; they continue to rely on the email-keyed UpsertUser
// path. Callers must type-assert.
type SubjectProvider interface {
	// UserInfoWithSubject is the SubjectProvider counterpart to UserInfo. It
	// returns the provider-stable user id (sub) in addition to the profile
	// fields. Email may be "" if the IdP did not surface one.
	//
	// state is the same anti-CSRF nonce that was passed to AuthCodeURL and
	// returned by the IdP in the callback URL. Most providers ignore it,
	// but PKCE-based flows (Twitter/X) use it to recompute the code_verifier
	// without persisting per-flow state on the server.
	UserInfoWithSubject(ctx context.Context, code, state string) (sub, email, name, avatarURL string, err error)
}
