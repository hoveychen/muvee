package auth

import (
	"context"
	"fmt"
	"os"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

type googleProvider struct {
	config   *oauth2.Config
	verifier *gooidc.IDTokenVerifier
}

func newGoogleProvider(redirectURL string) (*googleProvider, error) {
	clientID := os.Getenv("GOOGLE_CLIENT_ID")
	if clientID == "" {
		return nil, nil
	}
	clientSecret := os.Getenv("GOOGLE_CLIENT_SECRET")
	if redirectURL == "" {
		redirectURL = os.Getenv("GOOGLE_REDIRECT_URL")
	}
	if redirectURL == "" {
		redirectURL = "http://localhost:8080/auth/google/callback"
	}
	return newGoogleProviderFromConfig(GoogleConfig{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
	})
}

// GoogleConfig holds an admin-configured Google OAuth app, used by the
// downstream ForwardAuth path so subdomains can sign users in with a
// Google Cloud project distinct from the platform-side env-var app.
// Returned by muvee-server's /api/internal/oauth/social-providers when
// `google_enabled` = true in system_settings.
type GoogleConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// newGoogleProviderFromConfig constructs a Google provider from an
// explicit config, bypassing the env-var lookup. Returns (nil, nil) when
// ClientID/ClientSecret is empty so callers can treat that as "not
// configured" and fall back to the env path or another provider.
func newGoogleProviderFromConfig(cfg GoogleConfig) (*googleProvider, error) {
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, nil
	}
	ctx := context.Background()
	oidcProvider, err := gooidc.NewProvider(ctx, "https://accounts.google.com")
	if err != nil {
		return nil, fmt.Errorf("oidc provider: %w", err)
	}
	return &googleProvider{
		config: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Endpoint:     google.Endpoint,
			Scopes:       []string{gooidc.ScopeOpenID, "email", "profile"},
		},
		verifier: oidcProvider.Verifier(&gooidc.Config{ClientID: cfg.ClientID}),
	}, nil
}

func (p *googleProvider) Name() string        { return "google" }
func (p *googleProvider) DisplayName() string { return "Google" }
func (p *googleProvider) OrgScoped() bool     { return false }

func (p *googleProvider) AuthCodeURL(state string) string {
	return p.config.AuthCodeURL(state, oauth2.AccessTypeOnline)
}

func (p *googleProvider) UserInfo(ctx context.Context, code string) (email, name, avatarURL string, err error) {
	token, err := p.config.Exchange(ctx, code)
	if err != nil {
		return "", "", "", fmt.Errorf("exchange code: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return "", "", "", fmt.Errorf("no id_token")
	}
	idToken, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return "", "", "", fmt.Errorf("verify token: %w", err)
	}
	var claims struct {
		Email   string `json:"email"`
		Name    string `json:"name"`
		Picture string `json:"picture"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return "", "", "", fmt.Errorf("parse claims: %w", err)
	}
	return claims.Email, claims.Name, claims.Picture, nil
}
