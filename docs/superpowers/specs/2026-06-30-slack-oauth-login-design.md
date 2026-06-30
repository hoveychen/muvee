# Slack OAuth 登录接入 — 设计文档

日期：2026-06-30
状态：待评审

## 目标

接入 Slack 作为新的 OAuth 登录提供商，并让通过 Slack 登录的用户与此前用
Google / Lark（飞书）等**平台提供商**登录的用户，按 email **自动合并**为同一账号。

## 决策摘要

- **接入类别：平台提供商（环境变量配置）**，与 Google / 飞书 / 企微 / 钉钉同列，
  通过 `SLACK_CLIENT_ID` 等环境变量配置，启动时加载。不接入 `/admin/settings`
  后台动态配置路径（社交提供商路径）。
- **实现方式：完全镜像 `internal/auth/google.go`**。Slack 的 "Sign in with Slack"
  是标准 OpenID Connect，用 `go-oidc` 库发现端点并校验 `id_token`。
- 行为与 Google 保持一致：`OrgScoped() == false`、不做 `email_verified` 门控、
  头像取 `picture` claim（Slack 不返回时自然为空）。

## 身份合并（核心需求）

合并机制已存在，无需新增代码：

- `internal/store/store.go:59` `UpsertUser` 使用
  `INSERT ... ON CONFLICT (email) DO UPDATE`，`users.email` 上有 UNIQUE 约束。
- 所有平台提供商登录都走
  `handleOAuthCallback`（`cmd/muvee/authservice.go:918`）→ `upsertUserUpstream`
  → `UpsertUser` 的**按 email 落库**路径。
- Slack 作为平台提供商走同一路径，返回的 email 命中 `ON CONFLICT (email)` 时，
  会更新到 Google / Lark 已建立的同一行 → 自动合并。

### 合并边界（已知且接受）

1. 仅当 email 字符串**完全相同**时合并；不同邮箱 = 不同账号，无模糊匹配。
2. **不与社交提供商合并**（Discord / Apple / Facebook / Twitter）——那些走
   `oauth_accounts` 表按 `(provider, sub)` 绑定、`email` 存 NULL
   （`store.go:89` `EnsureUserByOAuth`），是刻意不按 email 合并的。Slack 只与
   Google / Lark / 企微 / 钉钉四个平台提供商互通。
3. 按 email 合并存在账号接管语义（持有匹配邮箱的 Slack 工作区用户可进入已有账号）。
   这是 Google ↔ Lark **现有就存在**的行为，本次保持一致，不额外加固。

## Slack OIDC 事实（已核实官方文档）

发现文档：`https://slack.com/.well-known/openid-configuration`
（`go-oidc` 的 `NewProvider(ctx, "https://slack.com")` 自动发现以下端点，无需硬编码）

- authorize：`https://slack.com/openid/connect/authorize`
- token：`https://slack.com/api/openid.connect.token`
- userInfo：`https://slack.com/api/openid.connect.userInfo`

`id_token` claims：

| Claim | 有无 | 用途 |
|-------|------|------|
| `sub` | 有（如 `U123ABC456`） | 平台路径不使用 |
| `email` | 有 | 合并键 |
| `email_verified` | 有 | 与 Google 一致，不门控 |
| `name` | 有 | 显示名 |
| `picture` | **无** | 解析但为空 → `avatarURL` 为空 |

scopes：`openid`、`email`、`profile`

## 改动清单

### 1. 新建 `internal/auth/slack.go`（镜像 `google.go`）

- `slackProvider` 实现 `Provider` 接口。
- `newSlackProvider(redirectURL string) (*slackProvider, error)`：
  - 读 `SLACK_CLIENT_ID`，为空返回 `(nil, nil)`（未配置 → 调用方跳过注册）。
  - 读 `SLACK_CLIENT_SECRET`。
  - `redirectURL` 为空时回落 `SLACK_REDIRECT_URL`，再回落本地默认
    `http://localhost:8080/auth/slack/callback`。
  - `gooidc.NewProvider(ctx, "https://slack.com")` + `Verifier`。
  - `oauth2.Config`：scopes `{openid, email, profile}`，Endpoint 用发现结果
    （`oidcProvider.Endpoint()`）。
- 方法：
  - `Name() -> "slack"`
  - `DisplayName() -> "Slack"`
  - `OrgScoped() -> false`
  - `AuthCodeURL(state)`：`config.AuthCodeURL(state)`
  - `UserInfo(ctx, code)`：Exchange → 取 `id_token` → `verifier.Verify` →
    解析 claims `{email, name, picture}` → 返回 `(email, name, picture, nil)`。

### 2. 在 `internal/auth/forwardauth.go` 注册

- 在 `NewForwardAuthProviders` 内，与现有四个提供商并列新增：
  ```go
  slackP, err := newSlackProvider(redirectBase + "/_oauth/slack")
  if err != nil { return nil, fmt.Errorf("slack: %w", err) }
  if slackP != nil { providers[slackP.Name()] = slackP }
  ```

### 3.（可选）`web/src/pages/Login.tsx` 加 Slack 图标

- `ProviderIcon` 的 `switch` 增加 `case 'slack'` 放官方四色 logo。
- 不加则走 `default` 兜底图标，功能不受影响。

## 自动获得（无需改动）

- 回调路由 `/_oauth/slack` 由通用处理器 `handleOAuthCallback`
  （`authservice.go:248` 注册的 `/_oauth/{provider}`）自动接管。
- `/_oauth/providers` 列表与登录页按钮自动出现 Slack。

## 测试

- 单元测试参考 `internal/auth/auth_test.go` 既有风格：
  - `newSlackProvider` 在缺 `SLACK_CLIENT_ID` 时返回 `(nil, nil)`。
  - `Name()` / `DisplayName()` / `OrgScoped()` 返回值正确。
- 合并行为由既有 `UpsertUser` 的 `ON CONFLICT (email)` 保证，已有覆盖；如缺，
  补一条「不同 provider 相同 email 落到同一 user 行」的 store 层测试。

## 不做（YAGNI）

- 不接入社交提供商后台动态配置路径。
- 不调用 `openid.connect.userInfo` 拉头像。
- 不新增 `email_verified` 门控或跨邮箱的账号关联 UI。
