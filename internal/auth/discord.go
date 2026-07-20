package auth

import (
	"context"
	"encoding/json"
	"fmt"

	"golang.org/x/oauth2"
)

// DiscordConfig holds the per-deployment Discord OAuth app credentials.
// Loaded from system_settings (not env vars) so admins can configure social
// providers at runtime without restarting the server. RedirectURL is NOT
// here — authservice computes it from its own FORWARD_AUTH_BASE_URL +
// "/_oauth/discord" in BuildSocialProviders.
type DiscordConfig struct {
	ClientID     string
	ClientSecret string
}

type discordProvider struct {
	config *oauth2.Config
}

// newDiscordProvider returns a Discord OAuth2 provider. Returns (nil, nil)
// when either credential field is empty -- callers treat that as "not
// configured" and skip registration.
func newDiscordProvider(cfg DiscordConfig, redirectURL string) (*discordProvider, error) {
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, nil
	}
	return &discordProvider{
		config: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  redirectURL,
			Endpoint: oauth2.Endpoint{
				AuthURL:  "https://discord.com/oauth2/authorize",
				TokenURL: "https://discord.com/api/oauth2/token",
			},
			Scopes: []string{"identify", "email"},
		},
	}, nil
}

func (p *discordProvider) Name() string                 { return "discord" }
func (p *discordProvider) DisplayName() string          { return "Discord" }
func (p *discordProvider) OrgScoped() bool              { return false }
func (p *discordProvider) CanonicalRedirectURL() string { return p.config.RedirectURL }

func (p *discordProvider) cfgFor(redirectURL string) *oauth2.Config {
	if redirectURL == "" {
		return p.config
	}
	c := *p.config
	c.RedirectURL = redirectURL
	return &c
}

func (p *discordProvider) AuthCodeURL(state, redirectURL string) string {
	return p.cfgFor(redirectURL).AuthCodeURL(state)
}

// UserInfo satisfies the Provider interface for callers that have not yet
// migrated to the SubjectProvider path. The Discord user id is dropped on
// this code path; identity will bind on email instead, which means a
// Discord user without a verified email will fail to upsert. Prefer
// UserInfoWithSubject for new code.
func (p *discordProvider) UserInfo(ctx context.Context, code, redirectURL string) (email, name, avatarURL string, err error) {
	_, email, name, avatarURL, err = p.UserInfoWithSubject(ctx, code, "", redirectURL)
	return
}

func (p *discordProvider) UserInfoWithSubject(ctx context.Context, code, _, redirectURL string) (sub, email, name, avatarURL string, err error) {
	token, err := p.cfgFor(redirectURL).Exchange(ctx, code)
	if err != nil {
		return "", "", "", "", fmt.Errorf("exchange code: %w", err)
	}
	client := p.config.Client(ctx, token)
	resp, err := client.Get("https://discord.com/api/users/@me")
	if err != nil {
		return "", "", "", "", fmt.Errorf("fetch user: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", "", "", "", fmt.Errorf("discord /users/@me returned %d", resp.StatusCode)
	}
	var u struct {
		ID         string `json:"id"`
		Username   string `json:"username"`
		GlobalName string `json:"global_name"`
		Avatar     string `json:"avatar"`
		Email      string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return "", "", "", "", fmt.Errorf("parse user: %w", err)
	}
	sub = u.ID
	email = u.Email
	name = u.GlobalName
	if name == "" {
		name = u.Username
	}
	if u.Avatar != "" {
		avatarURL = fmt.Sprintf("https://cdn.discordapp.com/avatars/%s/%s.png", u.ID, u.Avatar)
	}
	return sub, email, name, avatarURL, nil
}
