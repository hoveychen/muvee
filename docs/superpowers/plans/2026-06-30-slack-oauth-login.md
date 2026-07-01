# Slack OAuth 登录接入 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 接入 Slack 作为平台级 OAuth 登录提供商，支持按 email 自动合并已有账号，并可通过 `SLACK_TEAM_IDS` 白名单限定允许登录的工作区。

**Architecture:** Slack 的 "Sign in with Slack" 是标准 OpenID Connect。新增 `internal/auth/slack.go`，以 `internal/auth/google.go` 为模板，用 `go-oidc` 发现端点并校验 `id_token`；在 `UserInfo` 中额外校验 `https://slack.com/team_id` claim 是否在白名单内。在 `internal/auth/forwardauth.go` 注册该提供商。回调路由 `/_oauth/slack`、provider 列表、登录按钮均由现有通用逻辑自动接管。email 自动合并由现有 `UpsertUser` 的 `ON CONFLICT (email)` 保证，无需新代码。

**Tech Stack:** Go 1.26，`github.com/coreos/go-oidc/v3` v3.17.0，`golang.org/x/oauth2`；前端 React + TypeScript（`web/src/pages/Login.tsx`）。

## Global Constraints

- 设计文档：`docs/superpowers/specs/2026-06-30-slack-oauth-login-design.md`（本计划须与之一致）。
- provider 标识符固定为 `"slack"`，显示名 `"Slack"`。
- `OrgScoped()` 恒为 `false`（理由见 spec「OrgScoped 的归属」），不修改 `auth.go` 的静态 `isOrgScopedProvider` 列表。
- 环境变量名固定：`SLACK_CLIENT_ID`、`SLACK_CLIENT_SECRET`、`SLACK_REDIRECT_URL`、`SLACK_TEAM_IDS`。
- 本地默认回调：`http://localhost:8080/auth/slack/callback`（与 Google 一致）。
- OIDC issuer 固定为 `https://slack.com`；scopes 为 `openid`、`email`、`profile`。
- 不接入社交提供商后台动态配置路径；不调用 `openid.connect.userInfo` 拉头像；不传 authorize 的 `team` 参数。
- `SLACK_TEAM_IDS` 为空 = 不限制工作区；非空 = 强制白名单。

---

## File Structure

- **Create** `internal/auth/slack.go` — `slackProvider`（实现 `Provider`），构造函数、`parseTeamIDs`/`teamAllowed` 纯函数、OIDC 流程 + team_id 校验。
- **Create** `internal/auth/slack_test.go` — `parseTeamIDs`、`teamAllowed`、未配置返回 nil、元数据方法的单元测试。
- **Modify** `internal/auth/forwardauth.go` — 在 `NewForwardAuthProviders` 注册 slack。
- **Modify** `web/src/pages/Login.tsx` — `ProviderIcon` 增加 `case 'slack'` 官方 logo（可选增强）。

---

### Task 1: Slack provider + 纯函数 + 单元测试

**Files:**
- Create: `internal/auth/slack.go`
- Test: `internal/auth/slack_test.go`

**Interfaces:**
- Consumes: `Provider` 接口（`internal/auth/provider.go:6`）；`gooidc.NewProvider`、`gooidc.Config`、`gooidc.IDTokenVerifier`、`gooidc.ScopeOpenID`（`github.com/coreos/go-oidc/v3/oidc`）；`oauth2.Config`（`golang.org/x/oauth2`）。
- Produces:
  - `func newSlackProvider(redirectURL string) (*slackProvider, error)` — `SLACK_CLIENT_ID` 为空时返回 `(nil, nil)`。
  - `func parseTeamIDs(raw string) map[string]bool`
  - `func teamAllowed(allowed map[string]bool, teamID string) bool`
  - `slackProvider` 实现 `Provider`：`Name() == "slack"`、`DisplayName() == "Slack"`、`OrgScoped() == false`。

- [ ] **Step 1: 写失败测试** — 创建 `internal/auth/slack_test.go`

```go
package auth

import "testing"

func TestParseTeamIDs(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want map[string]bool
	}{
		{"empty", "", map[string]bool{}},
		{"single", "T1", map[string]bool{"T1": true}},
		{"multiple with spaces", " T1 , T2 ", map[string]bool{"T1": true, "T2": true}},
		{"ignores empty entries", "T1,,T2,", map[string]bool{"T1": true, "T2": true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseTeamIDs(c.raw)
			if len(got) != len(c.want) {
				t.Fatalf("parseTeamIDs(%q) len = %d, want %d (%v)", c.raw, len(got), len(c.want), got)
			}
			for k := range c.want {
				if !got[k] {
					t.Errorf("parseTeamIDs(%q) missing %q (got %v)", c.raw, k, got)
				}
			}
		})
	}
}

func TestTeamAllowed(t *testing.T) {
	cases := []struct {
		name    string
		allowed map[string]bool
		teamID  string
		want    bool
	}{
		{"empty allowlist passes everything", map[string]bool{}, "T1", true},
		{"empty allowlist passes empty team", map[string]bool{}, "", true},
		{"in allowlist", map[string]bool{"T1": true, "T2": true}, "T2", true},
		{"not in allowlist", map[string]bool{"T1": true}, "T9", false},
		{"empty team rejected when restricted", map[string]bool{"T1": true}, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := teamAllowed(c.allowed, c.teamID); got != c.want {
				t.Errorf("teamAllowed(%v, %q) = %v, want %v", c.allowed, c.teamID, got, c.want)
			}
		})
	}
}

func TestNewSlackProviderUnconfigured(t *testing.T) {
	t.Setenv("SLACK_CLIENT_ID", "")
	p, err := newSlackProvider("https://example.com/_oauth/slack")
	if err != nil {
		t.Fatalf("newSlackProvider err = %v, want nil", err)
	}
	if p != nil {
		t.Fatalf("newSlackProvider = %v, want nil when SLACK_CLIENT_ID unset", p)
	}
}

func TestSlackProviderMetadata(t *testing.T) {
	p := &slackProvider{}
	if p.Name() != "slack" {
		t.Errorf("Name() = %q, want slack", p.Name())
	}
	if p.DisplayName() != "Slack" {
		t.Errorf("DisplayName() = %q, want Slack", p.DisplayName())
	}
	if p.OrgScoped() != false {
		t.Errorf("OrgScoped() = true, want false")
	}
}
```

- [ ] **Step 2: 运行测试，确认编译失败**

Run: `go test ./internal/auth/ -run 'TestParseTeamIDs|TestTeamAllowed|TestNewSlackProvider|TestSlackProviderMetadata' -v`
Expected: FAIL — 编译错误，`undefined: parseTeamIDs` / `undefined: newSlackProvider` / `undefined: slackProvider`。

- [ ] **Step 3: 写实现** — 创建 `internal/auth/slack.go`

```go
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

func (p *slackProvider) Name() string        { return "slack" }
func (p *slackProvider) DisplayName() string { return "Slack" }
func (p *slackProvider) OrgScoped() bool     { return false }

func (p *slackProvider) AuthCodeURL(state string) string {
	return p.config.AuthCodeURL(state)
}

func (p *slackProvider) UserInfo(ctx context.Context, code string) (email, name, avatarURL string, err error) {
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
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./internal/auth/ -run 'TestParseTeamIDs|TestTeamAllowed|TestNewSlackProvider|TestSlackProviderMetadata' -v`
Expected: PASS（4 个测试函数全部通过）。

- [ ] **Step 5: 全包测试 + vet，确认无回归**

Run: `go test ./internal/auth/... && go vet ./internal/auth/...`
Expected: PASS，无 vet 报错。

- [ ] **Step 6: 提交**

```bash
git add internal/auth/slack.go internal/auth/slack_test.go
git commit -m "feat(auth): Slack OIDC provider with SLACK_TEAM_IDS workspace allowlist

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: 在 forwardauth.go 注册 Slack

**Files:**
- Modify: `internal/auth/forwardauth.go`（`NewForwardAuthProviders`，在 `return providers, nil` 之前插入）

**Interfaces:**
- Consumes: `newSlackProvider`（Task 1）、`redirectBase` 参数。
- Produces: 启动时 `providers["slack"]` 在 `SLACK_CLIENT_ID` 配置后可用。

- [ ] **Step 1: 写实现** — 在 `internal/auth/forwardauth.go` 中 `dingtalkP` 区块之后、`return providers, nil` 之前插入：

```go
	slackP, err := newSlackProvider(redirectBase + "/_oauth/slack")
	if err != nil {
		return nil, fmt.Errorf("slack: %w", err)
	}
	if slackP != nil {
		providers[slackP.Name()] = slackP
	}

```

插入后该函数尾部应为：

```go
	dingtalkP, err := newDingTalkProvider(redirectBase + "/_oauth/dingtalk")
	if err != nil {
		return nil, fmt.Errorf("dingtalk: %w", err)
	}
	if dingtalkP != nil {
		providers[dingtalkP.Name()] = dingtalkP
	}

	slackP, err := newSlackProvider(redirectBase + "/_oauth/slack")
	if err != nil {
		return nil, fmt.Errorf("slack: %w", err)
	}
	if slackP != nil {
		providers[slackP.Name()] = slackP
	}

	return providers, nil
}
```

- [ ] **Step 2: 编译 + 测试**

Run: `go build ./... && go test ./internal/auth/...`
Expected: 编译通过，测试 PASS。

- [ ] **Step 3: 验证注册行为（手动 sanity check，无需网络）**

说明：`SLACK_CLIENT_ID` 未设时 `newSlackProvider` 返回 `(nil, nil)`，`providers` 不含 `"slack"`，`NewForwardAuthProviders` 不报错。这条路径已被 Task 1 的 `TestNewSlackProviderUnconfigured` 覆盖；本步只需确认 `go build ./...` 通过即可。

- [ ] **Step 4: 提交**

```bash
git add internal/auth/forwardauth.go
git commit -m "feat(auth): register Slack provider in NewForwardAuthProviders

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: 前端 Slack 登录图标（可选增强）

**Files:**
- Modify: `web/src/pages/Login.tsx`（`ProviderIcon` 函数，`switch (id)` 内，`default` 之前）

**Interfaces:**
- Consumes: 后端 `/_oauth/providers` 返回的 `{ id: "slack", display_name: "Slack" }`。
- Produces: 登录页 Slack 按钮显示官方四色 logo（不加则走 `default` 兜底图标，功能不受影响）。

- [ ] **Step 1: 写实现** — 在 `web/src/pages/Login.tsx` 的 `ProviderIcon` 的 `case 'dingtalk':` 区块之后、`default:` 之前插入：

```tsx
    case 'slack':
      return (
        <svg width="18" height="18" viewBox="0 0 122.8 122.8">
          <path d="M25.8 77.6c0 7.1-5.8 12.9-12.9 12.9S0 84.7 0 77.6s5.8-12.9 12.9-12.9h12.9v12.9zm6.5 0c0-7.1 5.8-12.9 12.9-12.9s12.9 5.8 12.9 12.9v32.3c0 7.1-5.8 12.9-12.9 12.9s-12.9-5.8-12.9-12.9V77.6z" fill="#E01E5A"/>
          <path d="M45.2 25.8c-7.1 0-12.9-5.8-12.9-12.9S38.1 0 45.2 0s12.9 5.8 12.9 12.9v12.9H45.2zm0 6.5c7.1 0 12.9 5.8 12.9 12.9s-5.8 12.9-12.9 12.9H12.9C5.8 58 0 52.2 0 45.1s5.8-12.9 12.9-12.9h32.3z" fill="#36C5F0"/>
          <path d="M97 45.1c0-7.1 5.8-12.9 12.9-12.9s12.9 5.8 12.9 12.9S117 58 109.9 58H97V45.1zm-6.5 0c0 7.1-5.8 12.9-12.9 12.9s-12.9-5.8-12.9-12.9V12.9C64.7 5.8 70.5 0 77.6 0s12.9 5.8 12.9 12.9v32.2z" fill="#2EB67D"/>
          <path d="M77.6 97c7.1 0 12.9 5.8 12.9 12.9s-5.8 12.9-12.9 12.9-12.9-5.8-12.9-12.9V97h12.9zm0-6.5c-7.1 0-12.9-5.8-12.9-12.9s5.8-12.9 12.9-12.9h32.3c7.1 0 12.9 5.8 12.9 12.9s-5.8 12.9-12.9 12.9H77.6z" fill="#ECB22E"/>
        </svg>
      )
```

- [ ] **Step 2: 类型检查 / 构建**

Run: `cd web && npm run build`（若项目用其他命令如 `npm run typecheck`，以 `web/package.json` 的 scripts 为准）
Expected: 构建通过，无 TypeScript 报错。

- [ ] **Step 3: 提交**

```bash
git add web/src/pages/Login.tsx
git commit -m "feat(web): add Slack icon to login provider buttons

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## 配置说明（实现后写入运维文档 / .env 示例）

启用 Slack 登录所需环境变量（authservice 进程）：

```
SLACK_CLIENT_ID=<Slack app client id>
SLACK_CLIENT_SECRET=<Slack app client secret>
# 可选：覆盖默认回调；不设则用 FORWARD_AUTH_BASE_URL + /_oauth/slack
SLACK_REDIRECT_URL=https://<your-host>/_oauth/slack
# 可选：限定允许登录的工作区（逗号分隔的 team id）；不设则不限制
SLACK_TEAM_IDS=T0123ABC456,T0456DEF789
```

Slack app 配置：在 Slack App 的 "OAuth & Permissions" / "Sign in with Slack" 中，将 Redirect URL 设为 `{FORWARD_AUTH_BASE_URL}/_oauth/slack`，并启用 `openid`、`email`、`profile` 三个 user scope。

---

## Self-Review

**Spec coverage:**
- 平台提供商 + 环境变量配置 → Task 1（构造函数读 env）+ Task 2（注册）。✓
- 镜像 google.go 的 OIDC 实现 → Task 1。✓
- email 自动合并 → 现有 `UpsertUser`，无需新代码（spec「身份合并」节已说明）；计划不新增任务，符合 YAGNI。✓
- workspace 白名单（`SLACK_TEAM_IDS` + team_id 校验）→ Task 1（`parseTeamIDs`/`teamAllowed`/`UserInfo` 校验）。✓
- `OrgScoped()` 恒 false、不改静态 switch → Task 1（方法返回 false）+ Global Constraints。✓
- 不门控 email_verified、头像取 picture → Task 1（claims 不含 email_verified；返回 picture）。✓
- 回调路由 / providers 列表自动获得 → 无需任务（spec「自动获得」节）。✓
- 前端图标（可选）→ Task 3。✓

**Placeholder scan:** 无 TBD / TODO；所有代码步骤含完整代码；测试含完整断言。✓

**Type consistency:** `parseTeamIDs`/`teamAllowed`/`newSlackProvider`/`slackProvider` 在 Task 1 定义并在 Task 2 使用，签名一致；`Name()`→`"slack"` 与 Task 2 的 `providers[slackP.Name()]` 一致；前端 `case 'slack'` 与后端 provider id 一致。✓
