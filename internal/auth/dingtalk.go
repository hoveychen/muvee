package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
)

// dingtalkProvider implements DingTalk (钉钉) OAuth2 login.
// Required env vars: DINGTALK_CLIENT_ID, DINGTALK_CLIENT_SECRET, DINGTALK_REDIRECT_URL.
type dingtalkProvider struct {
	clientID     string
	clientSecret string
	redirectURL  string
}

func newDingTalkProvider(redirectURL string) (*dingtalkProvider, error) {
	clientID := os.Getenv("DINGTALK_CLIENT_ID")
	if clientID == "" {
		return nil, nil
	}
	if redirectURL == "" {
		redirectURL = os.Getenv("DINGTALK_REDIRECT_URL")
	}
	if redirectURL == "" {
		redirectURL = "http://localhost:8080/auth/dingtalk/callback"
	}
	return &dingtalkProvider{
		clientID:     clientID,
		clientSecret: os.Getenv("DINGTALK_CLIENT_SECRET"),
		redirectURL:  redirectURL,
	}, nil
}

func (p *dingtalkProvider) Name() string        { return "dingtalk" }
func (p *dingtalkProvider) DisplayName() string { return "钉钉" }
func (p *dingtalkProvider) OrgScoped() bool     { return true }

func (p *dingtalkProvider) AuthCodeURL(state string) string {
	params := url.Values{}
	params.Set("client_id", p.clientID)
	params.Set("response_type", "code")
	params.Set("scope", "openid")
	params.Set("prompt", "consent")
	params.Set("state", state)
	params.Set("redirect_uri", p.redirectURL)
	return "https://login.dingtalk.com/oauth2/auth?" + params.Encode()
}

func (p *dingtalkProvider) UserInfo(ctx context.Context, code string) (email, name, avatarURL string, err error) {
	accessToken, err := p.exchangeToken(ctx, code)
	if err != nil {
		return "", "", "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.dingtalk.com/v1.0/contact/users/me", nil)
	if err != nil {
		return "", "", "", fmt.Errorf("build user_info request: %w", err)
	}
	req.Header.Set("x-acs-dingtalk-access-token", accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", "", fmt.Errorf("user_info request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		UnionID   string `json:"unionId"`
		Nick      string `json:"nick"`
		AvatarURL string `json:"avatarUrl"`
		Email     string `json:"email"`
		Mobile    string `json:"mobile"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", "", fmt.Errorf("parse user_info: %w", err)
	}

	email = result.Email
	if email == "" {
		email = result.UnionID + "@dingtalk.local"
	}
	return email, result.Nick, result.AvatarURL, nil
}

func (p *dingtalkProvider) exchangeToken(ctx context.Context, code string) (string, error) {
	payload := map[string]string{
		"clientId":     p.clientID,
		"clientSecret": p.clientSecret,
		"code":         code,
		"grantType":    "authorization_code",
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.dingtalk.com/v1.0/oauth2/userAccessToken", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	var result struct {
		AccessToken string `json:"accessToken"`
		ErrCode     string `json:"code"`
		ErrMsg      string `json:"message"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse token response: %w", err)
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("dingtalk token error %s: %s", result.ErrCode, result.ErrMsg)
	}
	return result.AccessToken, nil
}
