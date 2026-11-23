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

:::warning 请提供登出入口
muvee 替你完成登录，但没法在你的页面上放登出按钮。开启了**启用身份验证**的项目，每个页面都应该链接到 muvee 在每个项目域名下提供的账户页：

```html
<a href="/_oauth/me">账户</a>
```

`/_oauth/me` 显示当前登录的身份，并提供**退出登录**和**换个账号**。直接链接到 `/_oauth/logout` 也可以。如果应用自己维护会话，还请按[自有会话的应用](#自有会话的应用)一节处理。
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

最简单的登出入口无需写代码：在应用自己的域名下链接到账户页 `/_oauth/me` 即可（Traefik 会把每个项目域名下的 `/_oauth/*` 交给认证网关）：

```html
<a href="/_oauth/me">账户</a>
```

账户页显示在本项目登录的账号和登录方式，并提供三个按钮：**退出登录**（`/_oauth/logout`）、**换个账号**（先退出，再显示登录页，不会借平台的中心会话悄悄登回去）和 **Muvee 控制台**。未登录时改为显示登录链接。该页不会被缓存，也不能被其他站点用 iframe 嵌入。

### 账户角标（无需写代码）

也可以让 muvee 替项目加上入口：开启 **页面角标**（项目设置 → 认证，或 `muveectl projects update PROJECT_ID --account-badge`）后，访客会在项目需要登录的页面右下角看到一个**账户**小链接，指向 `/_oauth/me`。访客可以把它收起（在该浏览器里记住）。开关推出之后新建的项目默认开启，之前已有的项目默认关闭。

链接由 muvee 本来就注入到 HTML 页面里的脚本（`/_embed-bridge.js`）添加；脚本向同一域名下的 `/_oauth/account-ui` 询问是否显示，访客的任何信息都不会写进你的 HTML。以下情况不会出现：

- iframe 内、公开页面、**认证豁免路径**里的路径；
- CSP 拦截了注入脚本的页面（例如 `script-src` 用 `strict-dynamic` 或只认 nonce）——muvee 不会改动你的 CSP；
- muvee 无法改写的 HTML：用 gzip 以外方式压缩的响应，或没有小写 `</head>` 的页面。

角标画在 Shadow DOM 里，嵌入截图时会被跳过。如果应用已有自己的账户菜单或登出按钮，建议关闭角标，改在自己的菜单里链接到 `/_oauth/me`。和页面上的任何元素一样，角标可以被页面自己的脚本隐藏或仿冒，所以它只是导航入口，不是安全边界。

想一键登出，直接链接到 `/_oauth/logout`：

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

登出会吊销本项目的会话及其所属的中心会话，因此由同一中心会话签出的其他项目会话会在下一次请求时失效（显示已退出页）：退出一个项目仍会同时退出该基域下的所有项目。它不会退出 OAuth 登录方式本身（如 Google），也不会退出 muvee 控制台。反过来则会联动：退出 muvee 控制台会同时作废该用户在所有设备、所有基域上的项目登录态，之后访问任一项目都需要重新登录。退出控制台还会作废该用户通过下文设备流获得的全部 Bearer 令牌（所有项目）；重新获取的方式见[令牌作废](#令牌作废)。

如果 `/_oauth/logout` 是从其他来源触发的（例如其他页面里嵌入的链接或图片，通过 `Sec-Fetch-Site` 头判断），muvee 会先显示确认页，用户点击确认后才登出。不带该头的请求（curl、旧浏览器）直接登出。

用户也可以在 muvee 控制台一次退出所有项目：**个人资料 → 子站登录 → 退出全部子站** 会结束该控制台账号在所有设备、所有基域上的全部项目会话（控制台登录和 CLI 令牌保留）。它只作用于当前登录控制台的账号；用户用其他邮箱账号登录的项目不受影响。

## 自有会话的应用

如果你的服务在 muvee 传入的身份之上，还自己维护登录会话（会话 Cookie、服务端 session 存储），需要额外遵守两条。

**1. 按顺序登出。** 应用的登出端点先清掉自己的会话，再 302 到 `/_oauth/logout`。只跳 `/_oauth/logout` 不够：muvee 会话结束了，但应用自己的 Cookie 还留在浏览器里。

```js
app.get('/logout', (req, res) => {
  req.session.destroy();               // 先清自己的会话
  res.clearCookie('app_sid');
  res.redirect('/_oauth/logout');      // 再交给 muvee 结束平台会话
});
```

**2. 每个请求都比对身份。** 大多数登出根本不会经过你的 `/logout`：用户退出控制台、在别的项目登出、使用「退出全部子站」，或者换另一个账号重新登录（Google 账号和飞书账号即使同名也是两个用户）。这些情况下应用的旧会话 Cookie 都还在，而下一个通过 ForwardAuth 的请求带的 `X-Forwarded-User` 可能已经是另一个人。请在每个请求上比对，身份变了就丢弃旧会话——否则新用户会拿到上一个用户的会话：

```js
app.use((req, res, next) => {
  const user = req.headers['x-forwarded-user'];
  if (req.session.user && req.session.user !== user) {
    req.session.destroy();             // 身份变了：丢弃旧会话
    return res.redirect(req.originalUrl);
  }
  next();
});
```

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

### 令牌作废

在 90 天有效期内，以下任一操作会让设备流令牌提前失效：

- 该用户退出 muvee 控制台；
- 该用户在控制台令牌设置页点击 **作废全部 CLI 令牌**（不会退出控制台，浏览器里的项目子站登录态也保留）。

两者都会作废该用户截至当时签发的全部设备流令牌（所有项目）；之后新获取的令牌正常可用。`muveectl` 使用的个人 / 项目 API 令牌不受影响。

被作废的令牌会收到 `401 Unauthorized`，带 `WWW-Authenticate: Bearer error="invalid_token"` 响应头和一个 JSON 正文，正文里给出该请求所访问主机的设备流地址——不会被重定向到登录页：

```json
{
  "error": "token_revoked",
  "message": "This CLI token has been revoked (console sign-out or \"revoke all CLI tokens\"). Run the device authorization flow again against https://myapp.example.com/_oauth/device/code to get a new token.",
  "device_code_url": "https://myapp.example.com/_oauth/device/code"
}
```

重新获取令牌：对同一主机从[第 1 步](#第-1-步--请求设备码)重新走一遍设备流。CLI 应把 `error: "token_revoked"`（或 Bearer 请求上的任何 401）视为"需要重新登录"，丢弃已保存的令牌并重新发起流程。

本功能上线之前签发的令牌，会一直有效到该用户下一次退出控制台或点击作废按钮为止；上线前的那次退出不算。
