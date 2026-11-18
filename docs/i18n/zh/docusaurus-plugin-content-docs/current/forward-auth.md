---
id: forward-auth
title: ForwardAuth 与项目访问控制
sidebar_position: 7
---

# ForwardAuth 与项目访问控制

muvee 支持为已部署的应用开启项目级身份验证。启用后，所有访问 `{project}.domain.com` 的用户都会先被重定向到你配置的 OAuth 提供商（Google、飞书、企业微信、钉钉等）进行登录。

## 工作原理

```
浏览器 → Traefik → ForwardAuth 边车服务 → OAuth 提供商
                         ↓
                    验证 JWT 会话 Cookie
                         ↓
                    检查邮箱域名是否在
                    project.auth_allowed_domains 中
                         ↓
                    200 OK → Traefik 转发请求
                    403 → 拒绝访问
```

## 为项目开启认证

在 **Project Detail → Config** 标签页中：

1. 将 **启用身份验证** 切换为启用
2. 可选：设置 **允许的邮箱域名**（逗号分隔，如 `company.com,partner.org`）
3. 保存并重新部署

容器启动时，部署 Agent 会为其附加以下 Traefik 标签：

```
traefik.http.middlewares.{proj}-auth.forwardauth.address=http://muvee-authservice:4181/verify?project={id}&domains=company.com
traefik.http.middlewares.{proj}-auth.forwardauth.authResponseHeaders=X-Forwarded-User,X-Forwarded-User-Name,X-Forwarded-User-Avatar,X-Forwarded-User-Provider
traefik.http.routers.{proj}.middlewares={proj}-auth
```

## 会话流程 {#session-flow}

每个项目子域名都有**各自独立**的会话。不再有在 `*.BASE_DOMAIN` 之间共享的 Cookie：在 `a.BASE_DOMAIN` 上拿到的会话在 `b.BASE_DOMAIN` 上无效，因此某个已部署的应用无法拿访客的会话去冒用其他应用。

1. 用户访问 `{project}.domain.com`
2. Traefik 携带请求头调用 ForwardAuth 边车服务
3. 边车服务检查子站会话 Cookie `__Host-muvee_fwd`（JWT，仅限该子域名，且绑定该项目）
4. 若 Cookie 缺失或已过期：页面加载会被 302 重定向到 `{project}.domain.com/_oauth/login`。该页面先向**中心登录主机**（即 apex，也就是 `FORWARD_AUTH_BASE_URL` 的主机）发起静默登录（`/_oauth/authorize?prompt=none`）：
   - 若 apex 上已有有效的 SSO 会话（`__Host-muvee_sso`），它会签发一个 60 秒有效的一次性授权码，子域名用它换取自己的 `__Host-muvee_fwd`，用户直接回到原始地址，**全程看不到登录页**。
   - 否则登录页在子域名上渲染（项目品牌、项目启用的登录方式、密码 / 短信表单）。点击某个提供商后，经 apex 的 `/_oauth/start` 跳转到提供商；回调仍然落在 apex 的 `/_oauth/<provider>`，由它签发 SSO 会话并把授权码带回子域名。
5. 之后访问第二个项目时重复第 1–4 步；只要 apex 上有 SSO 会话，且第二个项目启用了该 SSO 登录所用的提供商，就静默完成；否则走该项目自己的登录流程（显示登录页，或直接跳到其唯一的提供商）。
6. 后续请求：验证 JWT，检查邮箱域名，返回 `200`

提供商的重定向 URI 不变（`https://BASE_DOMAIN/_oauth/<provider>`），无需新增 URI。apex 不再提供登录页：`BASE_DOMAIN/_oauth/login` 返回 `400`，登录总是从用户正在访问的站点发起。授权码兑换失败（例如授权码过期）时登录页会重试，超过 2 次则显示错误页而不是循环。本次行为发布时正在进行中的登录会失效，用户重新点一次登录即可。

**登录 state。** 每次登录在子站（`__Host-fwd_state_<hash>`）和 apex（`__Host-fwd_apex_state_<hash>`）各有一个独立的短期 state cookie，名字由该次登录的 state 派生，所以同一浏览器同时发起的多次登录（两个标签页、浏览器预加载出的重复请求）不会互相覆盖。用户在提供商页面最多可停留 **60 分钟**。若回调到达 apex 时 state 对不上（已过期、已用过、来自另一个浏览器），提供商返回的授权码会被直接丢弃，浏览器被送回它最近一次发起登录的站点登录入口（apex 上的 `__Host-fwd_login_hint`，跳转前会重新校验它是同一基域下的 muvee 站点）重新登录——提供商通常仍处于登录状态，一闪而过。10 分钟内最多自动恢复 2 次；超出次数、或 apex 不知道目标站点时，显示一个提示页，请用户回到原网站重新登录。

**升级影响。** 会话令牌现在必须带上其签发对应的项目（`aud`），因此部署本版本后，所有现存的子站会话在下一次请求时都会被拒绝。每个用户首次访问任一项目时需经提供商登录一次，之后其他项目凭 apex 的 SSO 静默登录。本版本之前签发的设备流（CLI）令牌没有 `aud`，同样会被立即拒绝；CLI 用户需要对项目主机重新走一遍设备流。

| 凭证 | Cookie（生产） | 有效期 |
|------|----------------|--------|
| 子站会话 | 各项目主机上的 `__Host-muvee_fwd` | OAuth 登录：12 小时滑动（剩余不足 6 小时时重新签发）；密码 / 短信登录：固定 7 天，不续期 |
| 中心 SSO 会话 | 仅在 apex 上的 `__Host-muvee_sso` | 7 天滑动（最多每天续一次，保留最初登录时间） |

`__Host-` Cookie 由浏览器强制要求：仅限本主机（无 `Domain`）、`Secure`、`Path=/`。SSO Cookie 不会发送给任何项目。旧版本设置在基域上的 Cookie（`muvee_fwd_session` 等）不再写入，并会在登出、会话被拒绝、登录完成时删除。在纯 `http` 的开发平台上（`FORWARD_AUTH_BASE_URL` 以 `http://` 开头且 `FWD_INSECURE_DEV_COOKIES=1`），Cookie 回退为不带 `__Host-` 前缀的名称；生产环境不可用。

持续访问的用户会通过滑动窗口一直保持登录；子站会话过期后，只要 apex 的 SSO 仍有效就会静默续上，SSO 也过期时才会看到登录页。演示账号（密码）和短信登录的会话例外：它们直接在子域名上完成登录，不产生 SSO 会话（因此不会让用户在其他项目无感登录），并保持固定 7 天有效期：每次请求都不会重新校验它们背后的账号或手机号授权，固定有效期是收回授权的唯一手段。

邀请链接用法不变：邀请 token 随回跳地址带回，在验证时就地消费，不再通过 Cookie 传递。

### 登出

`GET {project}.domain.com/_oauth/logout`（可带 `?redirect=`，只允许本站路径或本平台域名下的 `https` 地址，否则落到 `/_oauth/logged-out`）会清除该站点的 Cookie，吊销本会话**及其所属的 SSO 会话**，并写入「已登出」提示。由同一次 SSO 签出的其他项目会话在下一次请求时失效并显示已登出页，因此在一个项目登出就等于登出该基域下的全部项目。它不会登出 Google 等身份提供商本身。存在该提示 Cookie 时，登录页会显示已登出页，而不会静默把用户重新登录。

若请求带有 `Sec-Fetch-Site` 且不是 `same-origin`（例如其他项目或外站页面上的登出链接），会先显示确认页，需用户点击确认。在地址栏直接输入或通过书签打开（`Sec-Fetch-Site: none`）同样会出确认页；同一项目页面上的登出链接则不会。控制台登出仍会让该用户的全部项目会话失效。

### 自定义域名与别名

仅支持在平台基域下的主机上登录。不属于任何基域的自定义域名无法通过 ForwardAuth 登录，基域的 apex 也不能登记为项目别名（server 会拒绝）。

会话缺失或过期时，页面加载（第 4 步）会被重定向到登录页；而页面脚本发起的 `fetch` / XHR
请求会收到 `401 Unauthorized`，带 `X-Muvee-Auth: login-required` 头和 JSON 响应体
`{"error":"login_required","login_url":"…"}`。如果把接口请求也重定向到 HTML 登录页，SPA
只会把 HTML 当 JSON 解析然后报错；401 则可以被识别和处理。每个项目页面都已经由注入的
[auth-guard](./embed-bridge#auth-guard) 处理它：自动刷新页面，让用户走一遍登录后回到原页面。

:::info OAuth 回调的路由方式
`BASE_DOMAIN/_oauth` 路径由 Traefik 直接路由至 `muvee-authservice`，而非 Web UI。apex 的 `/_oauth` 现在由 `muvee-server` 生成的高优先级中心路由接管（会清除 `X-Muvee-Aud`），`docker-compose.yml` 中 `muvee-authservice` 容器上的 Traefik labels 路由仍可保留。由于 Traefik 会优先匹配更精确的 `Host + Path` 规则，`BASE_DOMAIN/_oauth` 由 Auth 边车服务处理，其余 `BASE_DOMAIN` 路径仍正常路由至 `muvee-server`。`FORWARD_AUTH_BASE_URL` 必须是基域的 apex，因为 server 只为 apex 生成中心 `/_oauth` 路由。
:::

## Traefik Dashboard

Traefik dashboard（`traefik.BASE_DOMAIN`）被视为又一个独立子站，拥有自己独立的会话。`docker-compose.yml` / `docker-compose.server.yml` 模板把 `traefik.BASE_DOMAIN/_oauth` 路由到 `muvee-authservice`，并注入 `X-Muvee-Aud=host:traefik.BASE_DOMAIN`。项目子域名的可信 `X-Muvee-Aud` 由 `muvee-server` 注入，并覆盖客户端伪造的值；中心主机上该头会被清除。

## 公开项目

若项目未启用身份验证，则不会附加 ForwardAuth 中间件——该项目对所有人公开可访问。

## 面向服务开发者

如果你正在开发部署在 muvee 上的服务，想了解如何读取用户身份、实现登出或支持 CLI 访问，请参阅 [服务认证集成](./service-auth-integration) 指南。
