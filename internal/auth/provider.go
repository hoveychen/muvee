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
