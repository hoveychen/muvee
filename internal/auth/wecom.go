package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
)

// wecomProvider implements enterprise WeChat Work (企业微信) OAuth2 login via QR-code SSO.
// Required env vars: WECOM_CORP_ID, WECOM_CORP_SECRET, WECOM_AGENT_ID, WECOM_REDIRECT_URL.
type wecomProvider struct {
	corpID      string
	corpSecret  string
	agentID     string
	redirectURL string
}

func newWeComProvider() (*wecomProvider, error) {
	corpID := os.Getenv("WECOM_CORP_ID")
	if corpID == "" {
		return nil, nil
	}
	redirectURL := os.Getenv("WECOM_REDIRECT_URL")
	if redirectURL == "" {
		redirectURL = "http://localhost:8080/auth/wecom/callback"
	}
	return &wecomProvider{
		corpID:      corpID,
		corpSecret:  os.Getenv("WECOM_CORP_SECRET"),
		agentID:     os.Getenv("WECOM_AGENT_ID"),
		redirectURL: redirectURL,
	}, nil
}

func (p *wecomProvider) Name() string        { return "wecom" }
func (p *wecomProvider) DisplayName() string { return "企业微信" }

// AuthCodeURL redirects the user to the WeCom QR-code login page.
func (p *wecomProvider) AuthCodeURL(state string) string {
	params := url.Values{}
	params.Set("appid", p.corpID)
	params.Set("agentid", p.agentID)
	params.Set("redirect_uri", p.redirectURL)
	params.Set("state", state)
	return "https://open.work.weixin.qq.com/wwopen/sso/qrConnect?" + params.Encode()
}

func (p *wecomProvider) UserInfo(ctx context.Context, code string) (email, name, avatarURL string, err error) {
	accessToken, err := p.getCorpToken(ctx)
	if err != nil {
		return "", "", "", err
	}

	// Step 1: get the internal userid from the OAuth code
	params := url.Values{}
	params.Set("access_token", accessToken)
	params.Set("code", code)
	resp, err := http.DefaultClient.Do(mustGetRequest(ctx, "https://qyapi.weixin.qq.com/cgi-bin/auth/getuserinfo?"+params.Encode()))
	if err != nil {
		return "", "", "", fmt.Errorf("wecom getuserinfo: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var userInfoResp struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
		UserID  string `json:"UserId"`
	}
	if err := json.Unmarshal(body, &userInfoResp); err != nil {
		return "", "", "", fmt.Errorf("parse getuserinfo: %w", err)
	}
	if userInfoResp.ErrCode != 0 {
		return "", "", "", fmt.Errorf("wecom getuserinfo error %d: %s", userInfoResp.ErrCode, userInfoResp.ErrMsg)
	}
	if userInfoResp.UserID == "" {
		return "", "", "", fmt.Errorf("wecom: no internal UserID returned (non-member account not supported)")
	}

	// Step 2: get detailed user profile
	params2 := url.Values{}
	params2.Set("access_token", accessToken)
	params2.Set("userid", userInfoResp.UserID)
	resp2, err := http.DefaultClient.Do(mustGetRequest(ctx, "https://qyapi.weixin.qq.com/cgi-bin/user/get?"+params2.Encode()))
	if err != nil {
		return "", "", "", fmt.Errorf("wecom user/get: %w", err)
	}
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(resp2.Body)

	var userDetail struct {
		ErrCode  int    `json:"errcode"`
		ErrMsg   string `json:"errmsg"`
		Name     string `json:"name"`
		Avatar   string `json:"avatar"`
		Email    string `json:"email"`
		BizMail  string `json:"biz_mail"`
		UserID   string `json:"userid"`
	}
	if err := json.Unmarshal(body2, &userDetail); err != nil {
		return "", "", "", fmt.Errorf("parse user/get: %w", err)
	}
	if userDetail.ErrCode != 0 {
		return "", "", "", fmt.Errorf("wecom user/get error %d: %s", userDetail.ErrCode, userDetail.ErrMsg)
	}

	email = userDetail.BizMail
	if email == "" {
		email = userDetail.Email
	}
	if email == "" {
		email = userDetail.UserID + "@wecom.local"
	}
	return email, userDetail.Name, userDetail.Avatar, nil
}

func (p *wecomProvider) getCorpToken(ctx context.Context) (string, error) {
	params := url.Values{}
	params.Set("corpid", p.corpID)
	params.Set("corpsecret", p.corpSecret)
	resp, err := http.DefaultClient.Do(mustGetRequest(ctx, "https://qyapi.weixin.qq.com/cgi-bin/gettoken?"+params.Encode()))
	if err != nil {
		return "", fmt.Errorf("wecom gettoken: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse gettoken: %w", err)
	}
	if result.ErrCode != 0 {
		return "", fmt.Errorf("wecom gettoken error %d: %s", result.ErrCode, result.ErrMsg)
	}
	return result.AccessToken, nil
}

func mustGetRequest(ctx context.Context, rawURL string) *http.Request {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		panic(err)
	}
	return req
}
