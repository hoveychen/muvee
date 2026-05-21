package auth

import "fmt"

// SocialConfigs is the wire payload muvee-server returns from
// /api/internal/oauth/social-providers. authservice fetches this at startup
// and on /_oauth/internal/reload so admins can configure social providers
// at runtime via /admin/settings instead of restarting the container.
//
// nil fields mean "not configured / disabled" -- callers skip those.
type SocialConfigs struct {
	Google   *GoogleConfig   `json:"google,omitempty"`
	Discord  *DiscordConfig  `json:"discord,omitempty"`
	Apple    *AppleConfig    `json:"apple,omitempty"`
	Facebook *FacebookConfig `json:"facebook,omitempty"`
	Twitter  *TwitterConfig  `json:"twitter,omitempty"`
}

// SocialProviderMetadata returns the static (id, display_name) pairs for the
// five social providers admins can enable via /admin/settings. Used by the
// downstream-providers listing endpoint to render checkboxes WITHOUT having
// to actually instantiate the providers (which would require valid creds and
// — for Apple — a parseable .p8 PEM). Values must stay in sync with each
// provider's Name() / DisplayName() methods.
func SocialProviderMetadata() []ProviderInfo {
	return []ProviderInfo{
		{ID: "google", DisplayName: "Google"},
		{ID: "discord", DisplayName: "Discord"},
		{ID: "apple", DisplayName: "Apple"},
		{ID: "facebook", DisplayName: "Facebook"},
		{ID: "twitter", DisplayName: "X"},
	}
}

// BuildSocialProviders instantiates the social providers in cfg, returning
// a map keyed by Name() ready to be merged into fwdProviders. Each provider's
// OAuth callback URL is computed as "{forwardAuthBase}/_oauth/{provider}" —
// the same pattern as NewForwardAuthProviders, so admins do not configure
// it per-provider. Disabled or partially-configured providers (returned as
// nil from their constructors) are skipped silently. A construction error
// from any single provider aborts the whole build so misconfiguration is
// loud, not silent.
func BuildSocialProviders(forwardAuthBase string, cfg SocialConfigs) (map[string]Provider, error) {
	out := make(map[string]Provider)
	if cfg.Google != nil {
		p, err := newGoogleProviderFromConfig(*cfg.Google, forwardAuthBase+"/_oauth/google")
		if err != nil {
			return nil, fmt.Errorf("google: %w", err)
		}
		if p != nil {
			out[p.Name()] = p
		}
	}
	if cfg.Discord != nil {
		p, err := newDiscordProvider(*cfg.Discord, forwardAuthBase+"/_oauth/discord")
		if err != nil {
			return nil, fmt.Errorf("discord: %w", err)
		}
		if p != nil {
			out[p.Name()] = p
		}
	}
	if cfg.Apple != nil {
		p, err := newAppleProvider(*cfg.Apple, forwardAuthBase+"/_oauth/apple")
		if err != nil {
			return nil, fmt.Errorf("apple: %w", err)
		}
		if p != nil {
			out[p.Name()] = p
		}
	}
	if cfg.Facebook != nil {
		p, err := newFacebookProvider(*cfg.Facebook, forwardAuthBase+"/_oauth/facebook")
		if err != nil {
			return nil, fmt.Errorf("facebook: %w", err)
		}
		if p != nil {
			out[p.Name()] = p
		}
	}
	if cfg.Twitter != nil {
		p, err := newTwitterProvider(*cfg.Twitter, forwardAuthBase+"/_oauth/twitter")
		if err != nil {
			return nil, fmt.Errorf("twitter: %w", err)
		}
		if p != nil {
			out[p.Name()] = p
		}
	}
	return out, nil
}
