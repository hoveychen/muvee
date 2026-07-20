package auth

import (
	"context"
	"encoding/json"
	"fmt"

	"golang.org/x/oauth2"
)

// FacebookConfig holds the per-deployment Meta for Developers app
// credentials. Note: the `email` permission requires Meta App Review for
// production use; until the app is approved Facebook returns only the
// public_profile fields (id, name, picture). RedirectURL is NOT here —
// authservice computes it from its own FORWARD_AUTH_BASE_URL +
// "/_oauth/facebook" in BuildSocialProviders.
type FacebookConfig struct {
	ClientID     string
	ClientSecret string
}

type facebookProvider struct {
	config *oauth2.Config
}

func newFacebookProvider(cfg FacebookConfig, redirectURL string) (*facebookProvider, error) {
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, nil
	}
	return &facebookProvider{
		config: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  redirectURL,
			Endpoint: oauth2.Endpoint{
				AuthURL:  "https://www.facebook.com/v18.0/dialog/oauth",
				TokenURL: "https://graph.facebook.com/v18.0/oauth/access_token",
			},
			// email may silently get stripped pre-App-Review; that's OK,
			// the (provider, sub) path does not depend on it.
			Scopes: []string{"public_profile", "email"},
		},
	}, nil
}

func (p *facebookProvider) Name() string                 { return "facebook" }
func (p *facebookProvider) DisplayName() string          { return "Facebook" }
func (p *facebookProvider) OrgScoped() bool              { return false }
func (p *facebookProvider) CanonicalRedirectURL() string { return p.config.RedirectURL }

func (p *facebookProvider) cfgFor(redirectURL string) *oauth2.Config {
	if redirectURL == "" {
		return p.config
	}
	c := *p.config
	c.RedirectURL = redirectURL
	return &c
}

func (p *facebookProvider) AuthCodeURL(state, redirectURL string) string {
	return p.cfgFor(redirectURL).AuthCodeURL(state)
}

func (p *facebookProvider) UserInfo(ctx context.Context, code, redirectURL string) (email, name, avatarURL string, err error) {
	_, email, name, avatarURL, err = p.UserInfoWithSubject(ctx, code, "", redirectURL)
	return
}

func (p *facebookProvider) UserInfoWithSubject(ctx context.Context, code, _, redirectURL string) (sub, email, name, avatarURL string, err error) {
	token, err := p.cfgFor(redirectURL).Exchange(ctx, code)
	if err != nil {
		return "", "", "", "", fmt.Errorf("exchange code: %w", err)
	}
	client := p.config.Client(ctx, token)
	resp, err := client.Get("https://graph.facebook.com/v18.0/me?fields=id,name,email,picture.type(large)")
	if err != nil {
		return "", "", "", "", fmt.Errorf("fetch user: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", "", "", "", fmt.Errorf("facebook /me returned %d", resp.StatusCode)
	}
	var u struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Email   string `json:"email"`
		Picture struct {
			Data struct {
				URL string `json:"url"`
			} `json:"data"`
		} `json:"picture"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return "", "", "", "", fmt.Errorf("parse user: %w", err)
	}
	return u.ID, u.Email, u.Name, u.Picture.Data.URL, nil
}
