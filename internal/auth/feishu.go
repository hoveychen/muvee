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
	"strings"
)

// feishuProvider supports both Feishu (飞书, open.feishu.cn) and Lark (open.larksuite.com).
// Set FEISHU_BASE_URL=https://open.larksuite.com to use the international Lark endpoints.
type feishuProvider struct {
	appID       string
	appSecret   string
	redirectURL string
	baseURL     string // e.g. https://open.feishu.cn or https://open.larksuite.com
}

func newFeishuProvider() (*feishuProvider, error) {
	appID := os.Getenv("FEISHU_APP_ID")
	if appID == "" {
		return nil, nil
	}
	baseURL := os.Getenv("FEISHU_BASE_URL")
	if baseURL == "" {
		baseURL = "https://open.feishu.cn"
	}
	baseURL = strings.TrimRight(baseURL, "/")
	redirectURL := os.Getenv("FEISHU_REDIRECT_URL")
	if redirectURL == "" {
		redirectURL = "http://localhost:8080/auth/feishu/callback"
	}
	return &feishuProvider{
		appID:       appID,
		appSecret:   os.Getenv("FEISHU_APP_SECRET"),
		redirectURL: redirectURL,
		baseURL:     baseURL,
	}, nil
}

func (p *feishuProvider) Name() string        { return "feishu" }
func (p *feishuProvider) DisplayName() string { return "飞书 / Lark" }

func (p *feishuProvider) AuthCodeURL(state string) string {
	params := url.Values{}
	params.Set("app_id", p.appID)
	params.Set("redirect_uri", p.redirectURL)
	params.Set("scope", "contact:user.email:readonly")
	params.Set("state", state)
	return p.baseURL + "/open-apis/authen/v1/authorize?" + params.Encode()
}

func (p *feishuProvider) UserInfo(ctx context.Context, code string) (email, name, avatarURL string, err error) {
	accessToken, err := p.exchangeToken(ctx, code)
	if err != nil {
		return "", "", "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/open-apis/authen/v1/user_info", nil)
	if err != nil {
		return "", "", "", fmt.Errorf("build user_info request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", "", fmt.Errorf("user_info request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Name            string `json:"name"`
			AvatarURL       string `json:"avatar_url"`
			Email           string `json:"email"`
			EnterpriseEmail string `json:"enterprise_email"`
			OpenID          string `json:"open_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", "", fmt.Errorf("parse user_info: %w", err)
	}
	if result.Code != 0 {
		return "", "", "", fmt.Errorf("feishu user_info error %d: %s", result.Code, result.Msg)
	}

	email = result.Data.EnterpriseEmail
	if email == "" {
		email = result.Data.Email
	}
	if email == "" {
		email = result.Data.OpenID + "@feishu.local"
	}
	return email, result.Data.Name, result.Data.AvatarURL, nil
}

func (p *feishuProvider) exchangeToken(ctx context.Context, code string) (string, error) {
	payload := map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     p.appID,
		"client_secret": p.appSecret,
		"code":          code,
		"redirect_uri":  p.redirectURL,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/open-apis/authen/v2/oauth/token", bytes.NewReader(body))
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
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse token response: %w", err)
	}
	if result.Code != 0 {
		return "", fmt.Errorf("feishu token error %d: %s", result.Code, result.Msg)
	}
	return result.Data.AccessToken, nil
}
