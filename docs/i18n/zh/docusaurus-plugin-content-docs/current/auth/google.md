---
id: auth-google
title: Google OAuth2
sidebar_position: 1
---

# Google OAuth2

muvee 支持通过 **OpenID Connect (OIDC)** 接入 Google 登录。对于已使用 Google Workspace 或个人 Google 账号的团队，这是最简单的接入方式。

## 前置条件

- 一个已配置了 **OAuth 同意屏幕** 的 [Google Cloud](https://console.cloud.google.com/) 项目
- 控制平面可通过 HTTPS 访问（Google 的重定向 URI 策略要求）

## 配置步骤

### 1. 创建 OAuth2 凭证

1. 打开 [Google Cloud Console](https://console.cloud.google.com/apis/credentials) 的 **APIs & Services → 凭证**
2. 点击 **创建凭证 → OAuth 客户端 ID**
3. 应用类型选择 **Web 应用**
4. 在 **已授权的重定向 URI** 中添加**以下两个**（将 `example.com` 替换为你的 `BASE_DOMAIN`）：
   ```
   https://example.com/auth/google/callback
   https://example.com/_oauth
   ```
   第一个 URI 用于控制台登录；第二个用于 `muvee-authservice` 的 **ForwardAuth** 认证。
5. 点击 **创建**，并复制 **Client ID** 和 **Client Secret**

### 2. 配置环境变量

```env
GOOGLE_CLIENT_ID=your-client-id.apps.googleusercontent.com
GOOGLE_CLIENT_SECRET=your-client-secret
# 可选：仅当回调地址与默认值不同时才需要设置
# GOOGLE_REDIRECT_URL=https://example.com/auth/google/callback
```

### 3. （可选）限制登录邮箱域名

```env
# 仅允许以下域名的账号登录（逗号分隔）
ALLOWED_DOMAINS=your-company.com,partner.com
```

`ALLOWED_DOMAINS` 留空则允许任意 Google 账号登录。

## 环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `GOOGLE_CLIENT_ID` | — | OAuth2 客户端 ID |
| `GOOGLE_CLIENT_SECRET` | — | OAuth2 客户端密钥 |
| `GOOGLE_REDIRECT_URL` | `http://localhost:8080/auth/google/callback` | 在 Google Cloud Console 中注册的回调地址 |

## 工作原理

muvee 使用标准 OIDC 授权码流程：

1. 用户点击 **使用 Google 继续** → 跳转至 `accounts.google.com`
2. Google 将授权码返回至 `/auth/google/callback`
3. muvee 用授权码换取 ID Token，并通过 Google JWKS 验证签名
4. 从已验证的 ID Token 中读取 `email`、`name`、`picture` 字段
5. 校验邮箱是否符合 `ALLOWED_DOMAINS`（如已配置），然后在数据库中 upsert 用户，并签发有效期 7 天的 JWT 会话 Cookie

:::note ForwardAuth
`muvee-authservice` 使用相同的 Google 凭证，但配置独立的重定向地址（`FORWARD_AUTH_REDIRECT_URL`）。这让 Traefik 可以在不影响主控制台会话的情况下，为 project 子域名提供 Google 登录保护。
:::
