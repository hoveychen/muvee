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

muvee 自身的认证 Cookie（每个项目自己的会话 `__Host-muvee_fwd`、中心登录会话 `__Host-muvee_sso`、短期的登录辅助 Cookie，以及早期版本的旧名称）会在请求到达你的服务之前被移除，因此你的服务永远拿不到用户的会话。你自己的 Cookie 原样转发。需要用户信息时请在浏览器里调用 `/_oauth/*`（例如 `/_oauth/userinfo`），不要在后端转发会话 Cookie。

## 会话按项目隔离

每个项目域名都有自己的会话 Cookie `__Host-muvee_fwd`。它是 host-only（不带 `Domain`），浏览器不会把它发给其他子域；会话本身也只绑定该项目：**一个项目的会话在其他项目上无效**。特别是从 `a.example.com` 向 `b.example.com/_oauth/userinfo` 发起 `fetch(..., { credentials: "include" })`，不再带有可用的会话——请改为调用项目自己域名下的 `/_oauth/*`。

跨项目登录仍然无感。muvee 在基域的 apex 主机上另有一个中心登录会话（`__Host-muvee_sso`，7 天滑动），它不会发给任何项目。用户打开第二个项目时，该项目会静默向 apex 主机换取一次性授权码，并据此签出自己的会话——用户看不到登录页。通过 OAuth 登录的项目会话有效期 12 小时，使用中自动续期；密码 / 短信登录的会话固定 7 天、不续期，且不产生中心会话（因此不会让用户在其他项目无感登录）。

OAuth 提供商的回调仍落在 apex 主机，无需新增 redirect URI。apex 主机本身不再提供登录页。基域之外的自定义域名（别名）不支持登录，且基域的 apex 不能登记为项目别名。

**升级影响。** 会话令牌现在必须带上其签发对应的项目，因此升级后所有现存的项目会话会立即失效。每个用户下次访问任一项目时需经 provider 登录一次，之后其他项目无需再次提示即可登录。本版本之前签发的设备流令牌没有这一绑定，会被立即拒绝：CLI 用户需要对项目主机重新走一遍设备流。

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

浏览器中运行的前端 JavaScript 无法看到这些 Header——它们仅存在于 Traefik 和你的后端之间。要在前端获取用户信息，在应用自己的域名下调用认证网关的 userinfo 接口（用相对路径，即同源）：

```javascript
const resp = await fetch("/_oauth/userinfo", {
  credentials: "include",
});
if (resp.ok) {
  const { email, name, avatar_url, provider } = await resp.json();
}
```

未认证时返回 `401`。

在浏览器里也可以使用 `@muvee/auth` SDK（0.2.0）：`MuveeAuth.getUser()` 封装了 `/_oauth/userinfo`，`MuveeAuth.signOut()` 封装了 `/_oauth/logout`。

### 弹窗登录（`@muvee/auth` 0.2.0）

`signIn(provider)` 会打开一个弹窗，经中心登录主机直接进入所选 provider（不会显示项目登录页），并**同步**返回一个句柄：

```javascript
const handle = auth.signIn("google");   // 在点击事件里调用，不要放在 await 之后
const user = await handle.wait();       // 登录完成后得到用户信息
```

`handle` 包含 `url`（登录起始地址；`oauthUrl` 是已弃用的别名）、`popup`（弹窗窗口；被浏览器拦截时为 `null`，此时可让用户点链接打开 `handle.url`）、`wait()` 和 `cancel()`。`cancel()` 会关闭弹窗，并以 `AuthError('cancelled')` reject `wait()`。可选参数：`popup`（你在点击事件里预先打开的窗口）和 `timeoutMs`（默认 10 分钟，超时 `wait()` 以 `AuthError('expired')` reject）。登录完成后弹窗会通知你的页面，`wait()` 以 `getUser()` 的结果 resolve。

会话是在该浏览器中设置的 Cookie，因此自 0.2.0 起 `signIn` 不再支持 Tauri、React Native、Electron 等非浏览器宿主，请勿在其中调用。只有完全没有全局 `window` 的环境才会抛出 `AuthError('unsupported')`（若尚未配置 `baseUrl`，会先抛 `not_configured`）。`baseUrl` 必须与页面同源。`minPollIntervalSec` 已弃用，不再生效。从 0.1.x 升级：把 `await signIn(p)` 改为 `const h = signIn(p); await h.wait()`；0.1.x 的 `signIn` 在当前 muvee 上会让 `wait()` 以 `undefined` resolve 且不建立会话。

旧的 `POST /_oauth/login-token` 与 `/poll` 接口仍然保留，但轮询成功只返回 `{"status":"success"}`——永远不会下发身份或凭证。

## 登出

最简单的登出无需写代码：在应用自己的域名下链接到 `/_oauth/logout` 即可（Traefik 会把每个项目域名下的 `/_oauth/*` 交给认证网关）：

```html
<a href="/_oauth/logout">退出登录</a>
```

这会让用户退出本项目，并结束其在该基域上的中心登录会话，然后落到带项目品牌的「已退出登录」页，页上有**重新登录**按钮。它不会跳回 `/`：对只开启了一个 OAuth 登录方式的项目，跳回 `/` 会借助该登录方式仍然有效的会话立即把用户登回去。

如需登出后跳到其他页面，传入 `redirect`：

```
https://myapp.example.com/_oauth/logout?redirect=/goodbye
```

`redirect` 只能是本站路径（以 `/` 开头且不以 `//` 开头），或本平台域名下的 `https` 地址；含反斜杠或控制字符的值同样会被拒绝；其他值会被忽略，用户落到已退出页。

登出还会留下一个 `__Host-muvee_fwd_signed_out` 标记 Cookie（host-only，仅限本项目）。在用户重新登录之前，登录页会显示登录按钮，而不是直接跳到唯一的 OAuth 登录方式；因此登出后某个旧标签页自行刷新（例如 auth-guard 发现会话失效）时，不会悄悄把用户登回去。

登出会吊销本项目的会话及其所属的中心会话，因此由同一中心会话签出的其他项目会话会在下一次请求时失效（显示已退出页）：退出一个项目仍会同时退出该基域下的所有项目。它不会退出 OAuth 登录方式本身（如 Google），也不会退出 muvee 控制台。反过来则会联动：退出 muvee 控制台会同时作废该用户在所有设备、所有基域上的项目登录态，之后访问任一项目都需要重新登录。通过下文设备流获得的 Bearer 令牌不受影响；本版本之前签发的设备流令牌已不再被接受，请重新获取。

如果 `/_oauth/logout` 是从其他来源触发的（例如其他页面里嵌入的链接或图片，通过 `Sec-Fetch-Site` 头判断），muvee 会先显示确认页，用户点击确认后才登出。不带该头的请求（curl、旧浏览器）直接登出。

## CLI / Headless 访问（Device Flow）

对于需要访问认证保护服务但没有浏览器会话的 CLI 工具或 headless 环境，muvee 提供类似 `gh auth login` 的 Device Flow。请对项目自己的域名发起该流程（下文以 `myapp.example.com` 表示）：签出的令牌绑定该项目。

### 第 1 步 — 请求设备码

```bash
curl -X POST https://myapp.example.com/_oauth/device/code
```

```json
{
  "device_code": "XXXXXXXXXXX...",
  "user_code": "ABCD-1234",
  "verification_uri": "https://myapp.example.com/_oauth/device/activate",
  "verification_uri_complete": "https://myapp.example.com/_oauth/device/activate?code=ABCD-1234",
  "expires_in": 600,
  "interval": 5
}
```

### 第 2 步 — 引导用户授权

向用户输出包含完整 URL 的提示，让用户直接点击或复制打开：

```
请授权此设备：https://myapp.example.com/_oauth/device/activate?code=ABCD-1234
```

打开此 URL 后先显示设备登录页，页面会标明 CLI 将获得访问权限的主机；用户需手动点击某个 provider 按钮确认并继续 OAuth 登录。登录完成后浏览器显示确认页面，用户可以关闭标签页。

### 第 3 步 — 轮询获取 Token

```bash
curl -X POST https://myapp.example.com/_oauth/device/token \
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

Token 有效期 90 天，仅对发起设备流的那个项目有效，其他项目会拒绝它。
