package auth

import (
	"context"
	"fmt"
	"os"
	"strings"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// slackProvider implements Provider for Slack "Sign in with Slack" (OIDC).
// It mirrors googleProvider: OIDC discovery against the Slack issuer plus
// id_token verification. Unlike Google it can be restricted to one or more
// Slack workspaces via SLACK_TEAM_IDS -- when set, the team_id claim in the
// verified id_token must be in the allowlist or login is rejected.
type slackProvider struct {
	config       *oauth2.Config
	verifier     *gooidc.IDTokenVerifier
	allowedTeams map[string]bool // empty => no workspace restriction
}

// parseTeamIDs splits a comma-separated SLACK_TEAM_IDS value into a set,
// trimming whitespace and dropping empty entries. A blank input yields an
// empty (non-nil) map, which callers treat as "no restriction".
func parseTeamIDs(raw string) map[string]bool {
	out := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
		if id := strings.TrimSpace(part); id != "" {
			out[id] = true
		}
	}
	return out
}

// teamAllowed reports whether teamID passes the allowlist. An empty allowlist
// means no restriction, so everything passes.
func teamAllowed(allowed map[string]bool, teamID string) bool {
	if len(allowed) == 0 {
		return true
	}
	return allowed[teamID]
}

// newSlackProvider returns a Slack OIDC provider, or (nil, nil) when
// SLACK_CLIENT_ID is unset (treated as "not configured" by the caller). The
// empty-client-id check short-circuits before any network call, matching
// newGoogleProvider.
func newSlackProvider(redirectURL string) (*slackProvider, error) {
	clientID := os.Getenv("SLACK_CLIENT_ID")
	if clientID == "" {
		return nil, nil
	}
	clientSecret := os.Getenv("SLACK_CLIENT_SECRET")
	if redirectURL == "" {
		redirectURL = os.Getenv("SLACK_REDIRECT_URL")
	}
	if redirectURL == "" {
		redirectURL = "http://localhost:8080/auth/slack/callback"
	}
	ctx := context.Background()
	oidcProvider, err := gooidc.NewProvider(ctx, "https://slack.com")
	if err != nil {
		return nil, fmt.Errorf("oidc provider: %w", err)
	}
	return &slackProvider{
		config: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Endpoint:     oidcProvider.Endpoint(),
			Scopes:       []string{gooidc.ScopeOpenID, "email", "profile"},
		},
		verifier:     oidcProvider.Verifier(&gooidc.Config{ClientID: clientID}),
		allowedTeams: parseTeamIDs(os.Getenv("SLACK_TEAM_IDS")),
	}, nil
}

func (p *slackProvider) Name() string                 { return "slack" }
func (p *slackProvider) DisplayName() string          { return "Slack" }
func (p *slackProvider) OrgScoped() bool              { return false }
func (p *slackProvider) CanonicalRedirectURL() string { return p.config.RedirectURL }

func (p *slackProvider) cfgFor(redirectURL string) *oauth2.Config {
	if redirectURL == "" {
		return p.config
	}
	c := *p.config
	c.RedirectURL = redirectURL
	return &c
}

func (p *slackProvider) AuthCodeURL(state, redirectURL string) string {
	return p.cfgFor(redirectURL).AuthCodeURL(state)
}

func (p *slackProvider) UserInfo(ctx context.Context, code, redirectURL string) (email, name, avatarURL string, err error) {
	token, err := p.cfgFor(redirectURL).Exchange(ctx, code)
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
		TeamID  string `json:"https://slack.com/team_id"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return "", "", "", fmt.Errorf("parse claims: %w", err)
	}
	if !teamAllowed(p.allowedTeams, claims.TeamID) {
		return "", "", "", fmt.Errorf("slack workspace %q not allowed", claims.TeamID)
	}
	return claims.Email, claims.Name, claims.Picture, nil
}
