package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/url"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"
)

// AppleConfig holds the per-deployment Sign in with Apple credentials. Apple
// is unusual among OAuth2 providers in that the client_secret is NOT a fixed
// string -- it must be re-generated for each token request by signing an
// ES256 JWT with the .p8 private key downloaded from the Apple Developer
// console. ClientID here is Apple's Services ID (the web-flow analogue of an
// app bundle id).
type AppleConfig struct {
	ClientID      string
	TeamID        string
	KeyID         string
	PrivateKeyPEM string
	RedirectURL   string
}

type appleProvider struct {
	cfg        AppleConfig
	privateKey *ecdsa.PrivateKey
	verifier   *gooidc.IDTokenVerifier
}

func newAppleProvider(cfg AppleConfig) (*appleProvider, error) {
	if cfg.ClientID == "" || cfg.TeamID == "" || cfg.KeyID == "" || cfg.PrivateKeyPEM == "" {
		return nil, nil
	}
	block, _ := pem.Decode([]byte(cfg.PrivateKeyPEM))
	if block == nil {
		return nil, fmt.Errorf("apple: invalid .p8 PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("apple: parse PKCS8 .p8: %w", err)
	}
	pk, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("apple: .p8 is not an ECDSA private key")
	}
	ctx := context.Background()
	oidcProvider, err := gooidc.NewProvider(ctx, "https://appleid.apple.com")
	if err != nil {
		return nil, fmt.Errorf("apple: oidc discovery: %w", err)
	}
	return &appleProvider{
		cfg:        cfg,
		privateKey: pk,
		verifier:   oidcProvider.Verifier(&gooidc.Config{ClientID: cfg.ClientID}),
	}, nil
}

func (p *appleProvider) Name() string        { return "apple" }
func (p *appleProvider) DisplayName() string { return "Apple" }
func (p *appleProvider) OrgScoped() bool     { return false }

// AuthCodeURL builds Apple's authorize URL with response_mode=query so the
// callback works with the existing GET-based chi route. The trade-off is
// that we never see the user's name -- Apple only surfaces it in the
// form_post body on first sign-in. Users can edit their display name in
// the muvee profile page after sign-in.
func (p *appleProvider) AuthCodeURL(state string) string {
	v := url.Values{
		"response_type": {"code"},
		"client_id":     {p.cfg.ClientID},
		"redirect_uri":  {p.cfg.RedirectURL},
		"scope":         {"email"},
		"state":         {state},
		"response_mode": {"query"},
	}
	return "https://appleid.apple.com/auth/authorize?" + v.Encode()
}

// generateClientSecret signs a short-lived ES256 JWT that Apple accepts in
// place of a static client_secret. Apple caps expiry at 6 months but we use
// 5 minutes per call so a leaked key buys an attacker an even shorter
// window. The kid header points at the matching .p8 in the Apple Developer
// console.
func (p *appleProvider) generateClientSecret() (string, error) {
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": p.cfg.TeamID,
		"iat": now.Unix(),
		"exp": now.Add(5 * time.Minute).Unix(),
		"aud": "https://appleid.apple.com",
		"sub": p.cfg.ClientID,
	})
	token.Header["kid"] = p.cfg.KeyID
	return token.SignedString(p.privateKey)
}

func (p *appleProvider) UserInfo(ctx context.Context, code string) (email, name, avatarURL string, err error) {
	_, email, name, avatarURL, err = p.UserInfoWithSubject(ctx, code, "")
	return
}

func (p *appleProvider) UserInfoWithSubject(ctx context.Context, code, _ string) (sub, email, name, avatarURL string, err error) {
	clientSecret, err := p.generateClientSecret()
	if err != nil {
		return "", "", "", "", fmt.Errorf("sign client_secret: %w", err)
	}
	cfg := &oauth2.Config{
		ClientID:     p.cfg.ClientID,
		ClientSecret: clientSecret,
		RedirectURL:  p.cfg.RedirectURL,
		Endpoint: oauth2.Endpoint{
			AuthURL:   "https://appleid.apple.com/auth/authorize",
			TokenURL:  "https://appleid.apple.com/auth/token",
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}
	token, err := cfg.Exchange(ctx, code)
	if err != nil {
		return "", "", "", "", fmt.Errorf("exchange code: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return "", "", "", "", fmt.Errorf("apple: no id_token in token response")
	}
	idToken, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return "", "", "", "", fmt.Errorf("verify id_token: %w", err)
	}
	var claims struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return "", "", "", "", fmt.Errorf("parse id_token claims: %w", err)
	}
	return claims.Sub, claims.Email, "", "", nil
}
