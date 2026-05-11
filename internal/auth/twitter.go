package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"golang.org/x/oauth2"
)

// TwitterConfig holds the per-deployment X (Twitter) OAuth2 app credentials.
// The X OAuth2 endpoints require PKCE; verifierSecret is derived from
// ClientSecret so the same value is recomputed across server restarts.
//
// Note: the `email` scope is not exposed by the X API v2 OAuth2 endpoints;
// only Elevated/Pro access on the older API v1.1 can return email. This
// provider therefore relies entirely on the (provider, sub) path -- the
// sign-in works fine without an email.
type TwitterConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

type twitterProvider struct {
	config         *oauth2.Config
	verifierSecret []byte
}

func newTwitterProvider(cfg TwitterConfig) (*twitterProvider, error) {
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, nil
	}
	h := sha256.Sum256([]byte(cfg.ClientSecret))
	return &twitterProvider{
		config: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Endpoint: oauth2.Endpoint{
				AuthURL:   "https://twitter.com/i/oauth2/authorize",
				TokenURL:  "https://api.twitter.com/2/oauth2/token",
				AuthStyle: oauth2.AuthStyleInHeader,
			},
			Scopes: []string{"users.read", "tweet.read"},
		},
		verifierSecret: h[:],
	}, nil
}

func (p *twitterProvider) Name() string        { return "twitter" }
func (p *twitterProvider) DisplayName() string { return "X" }
func (p *twitterProvider) OrgScoped() bool     { return false }

// deriveVerifier returns a PKCE code_verifier deterministically derived from
// (state, verifierSecret). state appears in both the authorize URL and the
// callback URL, so this lets the server recompute the verifier in the
// callback without persisting any per-flow state. The verifier itself
// remains secret because verifierSecret is server-only.
func (p *twitterProvider) deriveVerifier(state string) string {
	mac := hmac.New(sha256.New, p.verifierSecret)
	mac.Write([]byte(state))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (p *twitterProvider) AuthCodeURL(state string) string {
	verifier := p.deriveVerifier(state)
	return p.config.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))
}

// UserInfo is unsupported on the email path: Twitter never exposes email
// via OAuth2 v2 and the PKCE flow needs state. Callers must use the
// SubjectProvider path. We return an error here so any caller that picks
// the wrong path fails loudly rather than silently inserting an empty user.
func (p *twitterProvider) UserInfo(ctx context.Context, code string) (string, string, string, error) {
	return "", "", "", fmt.Errorf("twitter requires the SubjectProvider path; use UserInfoWithSubject with state")
}

func (p *twitterProvider) UserInfoWithSubject(ctx context.Context, code, state string) (sub, email, name, avatarURL string, err error) {
	if state == "" {
		return "", "", "", "", fmt.Errorf("twitter PKCE requires the original state to recompute code_verifier")
	}
	verifier := p.deriveVerifier(state)
	token, err := p.config.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return "", "", "", "", fmt.Errorf("exchange code: %w", err)
	}
	client := p.config.Client(ctx, token)
	resp, err := client.Get("https://api.twitter.com/2/users/me?user.fields=profile_image_url,name,username")
	if err != nil {
		return "", "", "", "", fmt.Errorf("fetch user: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", "", "", "", fmt.Errorf("twitter /users/me returned %d", resp.StatusCode)
	}
	var u struct {
		Data struct {
			ID              string `json:"id"`
			Name            string `json:"name"`
			Username        string `json:"username"`
			ProfileImageURL string `json:"profile_image_url"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return "", "", "", "", fmt.Errorf("parse user: %w", err)
	}
	name = u.Data.Name
	if name == "" {
		name = u.Data.Username
	}
	return u.Data.ID, "", name, u.Data.ProfileImageURL, nil
}
