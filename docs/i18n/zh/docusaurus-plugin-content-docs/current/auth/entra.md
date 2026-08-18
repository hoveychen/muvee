---
id: auth-entra
title: Microsoft Entra ID
sidebar_position: 5
---

# Microsoft Entra ID

muvee 支持通过 **OpenID Connect** + **OAuth 2.0 授权码流**接入 Microsoft Entra ID（原 Azure AD）。
它是唯一一个用**同一套凭据**同时接入**两个平面**的登录方式：

| 平面 | 谁来登录 | 开关 |
|---|---|---|
| 平台 | muvee 控制台用户（`muvee_session`） | `platform_entra_login_enabled` |
| 项目 | 通过 ForwardAuth 访问项目子域的终端用户（`muvee_fwd_session`） | `entra_enabled` |

与 Google / 飞书 / 企业微信 / 钉钉不同，Entra **不用环境变量配置**：所有配置都在
**后台 → 设置**（存 `system_settings`），改完无需重新部署。`ENTRA_*` 环境变量只作为 UI 里留空字段的兜底。

:::tip 只想看一步步怎么点？
见 [Entra ID —— 管理员配置指南](auth-entra-admin-setup)，那一页带截图，从 Azure 应用注册讲到 muvee 后台设置卡片。
当前这一页是它背后的参考手册。
:::

## 配置步骤

### 1. 在 Azure 注册应用

1. 打开 [Azure 门户](https://portal.azure.com/) 的 **Microsoft Entra ID → 应用注册 → 新注册**
2. 按需选择 **支持的账户类型** —— 这决定了下面 tenant 该填什么
3. **重定向 URI** 选择 **Web** 平台，按需添加以下两个（把 `example.com` 换成你的 `BASE_DOMAIN`）：
   ```
   https://example.com/auth/entra/callback   # 平台登录
   https://example.com/_oauth/entra          # 项目子域（ForwardAuth）
   ```
   两个可以挂在同一个应用注册上——只加你要启用的那个平面对应的即可。

   :::note 多域名
   如果你在多个根域名下服务 muvee（`BASE_DOMAINS`），要为**每个**域名各加**两个** URI ——
   muvee 会把用户送回他发起登录的那个域名。详见 [配置 → 多域名](../configuration#多域名)。
   :::
4. 进入 **证书和密码 → 新客户端密码**，复制密码的 **值**（不是 ID）。
   Entra 的客户端密码**会过期**，记下过期日期——到期当天登录就会中断。

### 2. 在 muvee 里填写

在 **后台 → 设置 → Social Login Providers** 里找到 **Microsoft Entra ID** 卡片：

| 字段 | 从哪来 |
|---|---|
| Directory (tenant) ID | Azure 应用注册的 **概述** 页 |
| Application (client) ID | Azure 应用注册的 **概述** 页 |
| Client Secret | 上面第 4 步复制的密码**值** |

然后勾选你要的平面并**保存**：

- **Enable on project subdomains** —— muvee-server 会立刻让 muvee-authservice 重载 provider 集合，
  项目登录页马上出现按钮，无需重启。
- **Enable on the muvee platform login page** —— 在进程内注册，同样无需重启。

卡片里两个回调 URI 是只读的，带复制按钮，可直接粘进 Azure。

### 3. 选择哪些项目开放该登录方式

下游开启只是让它**可用**；每个项目的 **Auth** 标签页决定自己的登录页显示什么。
**Sign-in providers** 留空表示提供所有已配置的方式；要限制就勾选一个子集（现在多了 Microsoft）。

## tenant 的三种取值及其含义

`entra_tenant_id` 接受三种形态，选择会直接影响 muvee 校验 token 的严格程度：

| 取值 | 谁能登录 | ID token 校验 | `ALLOWED_DOMAINS` |
|---|---|---|---|
| 目录 GUID（**推荐**） | 仅该目录 | issuer **且** `tid` 必须等于所配 tenant | 跳过（org-scoped） |
| 已验证域名（`contoso.onmicrosoft.com`） | 仅该目录 | issuer 必须与其解析出的 `tid` 一致 | 跳过（org-scoped） |
| `organizations` / `common` / `consumers` | 任意工作/学校目录（后两者还含个人账号） | issuer 必须与 token 的 `tid` 一致 | **强制生效** |

三种情况下 audience 都必须等于你的 client ID，ID token 签名都会用
`login.microsoftonline.com` 上该 tenant 的 JWKS 验证。

单租户配置被视为 **org-scoped**——和飞书 / 企业微信 / 钉钉同一个语义：能进这个目录本身就是授权，
因此平台的 `ALLOWED_DOMAINS` 邮箱域名白名单会被跳过。而用多租户别名时，全世界任何人都能走完流程，
`ALLOWED_DOMAINS`（以及 access_mode）就是你唯一的门禁——**务必设置**。

## muvee 读取哪些 claim

| Claim | 用途 |
|---|---|
| `email`，回退到 `preferred_username`（UPN） | 平台 / 项目身份 |
| `name` | 显示名（合成的那个——`given_name` / `family_name` 属 [optional claims](https://learn.microsoft.com/zh-cn/entra/identity-platform/optional-claims)，muvee 也没有单独字段存） |
| `oid` + `tid` | 稳定的 subject key |
| `tid`、`iss`、`aud` | 校验（见上） |

`name` 和 `oid` 需要 `openid profile` scope，`email` 需要 `email` scope——muvee 都请求了。
`email` claim 只对 guest 账号默认下发；租户内成员靠 `email` scope 才有，
若目录里该用户没有 mail 属性，muvee 会回退用 `preferred_username` 里的 UPN。

### 头像

Entra v2.0 的 ID token **没有** `picture` claim，而 Graph 的头像接口需要 bearer token、没有公开 URL。
所以当 **Fetch profile photos** 开启时（默认开），muvee 会在登录请求里追加 Graph 的委派权限
`User.Read`，并在换取 token 之后立刻读 `GET /me/photos/96x96/$value`，把图片以 `data:` URI 内联存到用户记录上。

这是 best-effort：用户没设过头像（Graph 返回 `404`）、Graph 慢、或者缺少授权，都只会让头像为空，
**绝不会导致登录失败**。关掉这个开关会把 `User.Read` 从授权请求里彻底去掉——租户不给该 scope 授权时就该这么做。

## 设置项速查

| 设置 | 取值 | 说明 |
|---|---|---|
| `entra_enabled` | `true` / `false` | 在项目子域提供 Entra 登录 |
| `platform_entra_login_enabled` | `true` / `false` | 在 muvee 平台登录页提供 Entra 登录 |
| `entra_tenant_id` | GUID / 域名 / `common` / `organizations` / `consumers` | Azure 目录 |
| `entra_client_id` | — | 应用（客户端）ID |
| `entra_client_secret` | — | 客户端密码的**值** |
| `entra_avatar_enabled` | `true` / `false`（默认 `true`） | 从 Microsoft Graph 读头像；会追加 `User.Read` scope |

环境变量兜底（仅当对应设置留空时生效）：`ENTRA_TENANT_ID`、`ENTRA_CLIENT_ID`、`ENTRA_CLIENT_SECRET`、
`ENTRA_FETCH_AVATAR`，以及 `ENTRA_REDIRECT_URL`（覆盖推导出的平台回调）和 `PLATFORM_ENTRA_LOGIN`（平台开关的兜底）。

客户端密码在 `system_settings` 里是明文存储，与其他 provider 凭据同一威胁模型——
防线是「设置接口仅 admin 可写」。

## 排障

**`AADSTS50011: redirect URI mismatch`** —— Azure 里的 URI 必须逐字节一致，包括 `https://` 和末尾路径。
请从设置卡片里复制，不要手打。

**保存后按钮没出现** —— 凭据不全（tenant / client ID / 密码任一为空都会故意不注册），或 tenant 格式非法；
查 muvee-server 日志里的 `entra provider`。项目页还要确认该项目的 **Sign-in providers** 没有把它排除。

**`id_token was issued by tenant …, not the configured …`** —— 应用注册是多租户，而 `entra_tenant_id`
锁定了单个 GUID。要么把 tenant 改成实际签发 token 的那个目录，要么改成 `organizations`。

**原来能登，突然全员登不上** —— 查 Azure 里客户端密码是否过期。

**所有人都没有头像** —— 大概率是租户没给 `User.Read` 授权，服务端日志会打 `entra: fetch avatar`。
关掉 **Fetch profile photos** 即可不再申请该 scope。个别用户没头像只是空头像，无需处理。
