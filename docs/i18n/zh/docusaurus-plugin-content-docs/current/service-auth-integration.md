---
id: service-auth-integration
title: 服务认证集成
sidebar_position: 8
---

# 服务认证集成

本指南面向在 muvee 上部署服务的开发者，帮助你利用 muvee 内置的认证能力。当项目开启 **启用身份验证** 后，muvee 负责完整的 OAuth 登录流程——你的服务收到的请求中已包含用户身份信息。

:::note 认证而非授权
muvee 告诉你的服务**用户是谁**（authentication，认证）。它**不**管理每个用户能做什么——那是授权（authorization），由你的服务自行负责。例如，muvee 会告诉你"这个请求来自 `alice@company.com`"，但你的服务自己决定 Alice 是否能访问某个资源。
:::

## 读取用户身份（服务端）

每个已认证的请求到达你的服务时，都会包含以下 Header，由 Traefik 在 muvee 认证网关验证用户后注入：

| Header | 值 | 是否始终存在 |
|--------|---|-------------|
| `X-Forwarded-User` | 邮箱地址（如 `alice@company.com`） | 是 |
| `X-Forwarded-User-Name` | 显示名称（如 `Alice Zhang`） | 取决于 OAuth 提供商是否返回 |
| `X-Forwarded-User-Avatar` | 头像图片 URL | 取决于 OAuth 提供商是否返回 |
| `X-Forwarded-User-Provider` | OAuth 提供商名称（如 `google`、`feishu`） | 是 |

这些 Header 由 Traefik 设置，而非终端用户——只要你的服务仅通过 Traefik 可达，它们就**无法被伪造**。你的服务无需解析 cookie、验证 JWT 或与任何 OAuth 提供商通信。

```python
# Python / Flask
email    = request.headers.get("X-Forwarded-User")
name     = request.headers.get("X-Forwarded-User-Name")
avatar   = request.headers.get("X-Forwarded-User-Avatar")
provider = request.headers.get("X-Forwarded-User-Provider")
```

```go
// Go
email    := r.Header.Get("X-Forwarded-User")
name     := r.Header.Get("X-Forwarded-User-Name")
avatar   := r.Header.Get("X-Forwarded-User-Avatar")
provider := r.Header.Get("X-Forwarded-User-Provider")
```

```javascript
// Node.js / Express
const email    = req.headers["x-forwarded-user"];
const name     = req.headers["x-forwarded-user-name"];
const avatar   = req.headers["x-forwarded-user-avatar"];
const provider = req.headers["x-forwarded-user-provider"];
```

## 读取用户身份（前端）

浏览器中运行的前端 JavaScript 无法看到这些 Header——它们仅存在于 Traefik 和你的后端之间。要在前端获取用户信息，调用认证网关的 userinfo 接口：

```javascript
const resp = await fetch("https://{BASE_DOMAIN}/_oauth/userinfo", {
  credentials: "include",
});
if (resp.ok) {
  const { email, name, avatar_url, provider } = await resp.json();
}
```

未认证时返回 `401`。

## 登出

将用户重定向到认证网关的登出端点：

```
https://{BASE_DOMAIN}/_oauth/logout?redirect=https://myapp.example.com
```

这会清除 `muvee_fwd_session` Cookie，并将用户重定向到 `redirect` 指定的 URL（省略时默认为 `/`）。

## CLI / Headless 访问（Device Flow）

对于需要访问认证保护服务但没有浏览器会话的 CLI 工具或 headless 环境，muvee 提供类似 `gh auth login` 的 Device Flow。

### 第 1 步 — 请求设备码

```bash
curl -X POST https://{BASE_DOMAIN}/_oauth/device/code
```

```json
{
  "device_code": "XXXXXXXXXXX...",
  "user_code": "ABCD-1234",
  "verification_uri": "https://{BASE_DOMAIN}/_oauth/device/activate",
  "verification_uri_complete": "https://{BASE_DOMAIN}/_oauth/device/activate?code=ABCD-1234",
  "expires_in": 600,
  "interval": 5
}
```

### 第 2 步 — 引导用户授权

向用户输出包含完整 URL 的提示，让用户直接点击或复制打开：

```
请授权此设备：https://{BASE_DOMAIN}/_oauth/device/activate?code=ABCD-1234
```

打开此 URL 后用户会进入 OAuth 登录页面。登录完成后浏览器显示确认页面，用户可以关闭标签页。

### 第 3 步 — 轮询获取 Token

```bash
curl -X POST https://{BASE_DOMAIN}/_oauth/device/token \
  -H "Content-Type: application/json" \
  -d '{"device_code": "XXXXXXXXXXX..."}'
```

每隔 `interval` 秒轮询一次。用户未授权时返回：

```json
{"error": "authorization_pending"}
```

授权完成后：

```json
{
  "access_token": "eyJhbGciOiJI...",
  "token_type": "Bearer",
  "expires_in": 7776000
}
```

### 第 4 步 — 使用 Token

在每个请求中携带 Bearer Token：

```bash
curl -H "Authorization: Bearer eyJhbGciOiJI..." https://myapp.example.com/api/data
```

Token 有效期 90 天，适用于同一 `BASE_DOMAIN` 下所有开启认证的项目。
