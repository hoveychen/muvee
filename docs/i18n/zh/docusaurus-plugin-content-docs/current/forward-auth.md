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

1. 用户访问 `{project}.domain.com`
2. Traefik 携带请求头调用 ForwardAuth 边车服务
3. 边车服务检查 `muvee_fwd_session` Cookie（JWT）
4. 若 Cookie 缺失或已过期：重定向到配置的 OAuth 提供商（回调地址为 `BASE_DOMAIN/_oauth`）
5. 登录完成后：设置域级 JWT Cookie（在所有 `*.BASE_DOMAIN` 子域名间共享），重定向回原始访问地址
6. 后续请求：验证 JWT，检查邮箱域名，返回 `200`

会话有效期 7 天，并且滑动续期：会话签发满一天后，下一次通过 ForwardAuth 的请求会重新签发
cookie，有效期重置为 7 天。只要用户持续访问就一直保持登录，连续 7 天不访问才需要重新登录。演示账号（密码）和短信登录的会话
例外，仍是固定 7 天：每次请求都不会重新校验它们背后的账号或手机号授权，固定有效期是收回授权的唯一手段。

会话缺失或过期时，页面加载（第 4 步）会被重定向到登录页；而页面脚本发起的 `fetch` / XHR
请求会收到 `401 Unauthorized`，带 `X-Muvee-Auth: login-required` 头和 JSON 响应体
`{"error":"login_required","login_url":"…"}`。如果把接口请求也重定向到 HTML 登录页，SPA
只会把 HTML 当 JSON 解析然后报错；401 则可以被识别和处理。每个项目页面都已经由注入的
[auth-guard](./embed-bridge#auth-guard) 处理它：自动刷新页面，让用户走一遍登录后回到原页面。

:::info OAuth 回调的路由方式
`BASE_DOMAIN/_oauth` 路径由 Traefik 直接路由至 `muvee-authservice`，而非 Web UI。这通过 `docker-compose.yml` 中 `muvee-authservice` 容器的 Traefik labels 实现。由于 Traefik 会优先匹配更精确的 `Host + Path` 规则，`BASE_DOMAIN/_oauth` 由 Auth 边车服务处理，其余 `BASE_DOMAIN` 路径仍正常路由至 `muvee-server`。
:::

## 公开项目

若项目未启用身份验证，则不会附加 ForwardAuth 中间件——该项目对所有人公开可访问。

## 面向服务开发者

如果你正在开发部署在 muvee 上的服务，想了解如何读取用户身份、实现登出或支持 CLI 访问，请参阅 [服务认证集成](./service-auth-integration) 指南。
