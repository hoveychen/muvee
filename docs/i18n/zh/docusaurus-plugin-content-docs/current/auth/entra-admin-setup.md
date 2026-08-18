---
id: auth-entra-admin-setup
title: Entra ID —— 管理员配置指南
sidebar_position: 6
---

# Entra ID —— 管理员配置指南

给需要在 muvee 上把 Microsoft Entra ID 登录跑通的管理员看的逐步指南。muvee 这一侧全部在网页后台完成
——不用 SSH、不用重新部署、不用改环境变量。

背后的参考资料（tenant 语义、token 校验、muvee 读哪些 claim）见 [Microsoft Entra ID](auth-entra)。

## 开始之前

| | |
|---|---|
| Azure 侧 | 有在租户里创建应用注册的权限（应用程序开发者角色或更高） |
| muvee 侧 | 一个平台管理员账号（你能看到 **后台 → 设置**） |
| 先决定 | 要开哪些平面：muvee 控制台登录、项目子域、还是两个都开 |

## 第 1 步 —— 在 Azure 注册应用

1. [Azure 门户](https://portal.azure.com/) → **Microsoft Entra ID** → **应用注册** → **新注册**。
2. **名称**：好认就行，例如 `muvee`。
3. **支持的账户类型**：公司自用请选*仅此组织目录中的账户*（单租户）。这是最安全的选项——
   muvee 会拒绝任何非该租户签发的 token。
4. **重定向 URI**：平台选 **Web**，填入平台回调（确切值见第 3 步，也可以之后再补）：
   ```
   https://<你的 muvee 域名>/auth/entra/callback
   ```
5. 点 **注册**，然后从 **概述** 页复制：
   - **应用程序（客户端）ID**
   - **目录（租户）ID**

### 补上第二个重定向 URI（项目登录需要）

仍在该应用注册里 → **身份验证** → **Web → 添加 URI**：

```
https://<你的 muvee 域名>/_oauth/entra
```

这个用于项目子域上的登录页。如果你在多个根域名下服务 muvee（`BASE_DOMAINS`），要为**每个**域名各加
**两个** URI —— muvee 会把用户送回他发起登录的那个域名。

### 创建客户端密码

**证书和密码** → **客户端密码** → **新客户端密码**。选一个有效期，然后**立刻**复制密码的**值**
（离开页面后门户就不再显示）。记下过期日期——到期当天所有人都会登不上。

:::tip 关于头像
头像来自一次 Microsoft Graph 调用，用的是委派权限 **User.Read**，新建的应用注册在
**API 权限** 里默认就有。如果你的租户把它移除了、或者需要你拿不到的管理员同意，
那就别动 Azure，改为在第 3 步里取消勾选头像选项。
:::

## 第 2 步 —— 打开 muvee 后台设置

以管理员身份登录 muvee → **后台 → 设置** → 下滑到 **Social Login Providers (downstream sign-in)**
→ 找到 **Microsoft Entra ID** 卡片。

## 第 3 步 —— 填写卡片

![muvee 后台设置里的 Microsoft Entra ID 卡片](/img/entra-admin-settings-card.png)

*两个重定向 URI 是只读的，右侧有复制按钮。平台那个显示的始终是你当前访问 muvee 用的域名——
截图里是本地开发服务器；在你的部署上它会显示 `https://<你的 muvee 域名>/auth/entra/callback`。*

| 控件 | 填什么 |
|---|---|
| **Enable on project subdomains** | 勾选后，项目（ForwardAuth）登录页会出现 Microsoft |
| **Enable on the muvee platform login page** | 勾选后，登录 muvee 自身也能用它 |
| **Fetch profile photos from Microsoft Graph** | 想要头像就保持勾选；拿不到 `User.Read` 授权就取消 |
| **Directory (tenant) ID** | 第 1 步的租户 GUID |
| **Application (client) ID** | 第 1 步的客户端 ID |
| **Client Secret** | 第 1 步的密码**值** |
| **Redirect URI — platform login** | 只读；复制进 Azure |
| **Redirect URI — project subdomains** | 只读；复制进 Azure |

点 **Save**。两个平面都会立即生效——muvee 在进程内重建平台 provider 集合，同时把新配置推给
muvee-authservice，不涉及任何重启。

## 第 4 步 —— 验证

**平台登录**：用隐私窗口打开 muvee 登录页，应该能看到 **Continue with Microsoft** 按钮：

![平台登录页出现 Continue with Microsoft](/img/entra-login-button.png)

用租户里的账号登一次。之后到 **后台 → 用户** 里确认：该账号应带着显示名和邮箱出现，
开了头像的话还会带着 Entra 里的头像。

**项目登录**：打开一个开了鉴权的项目子域。若该项目 **Auth → Sign-in providers** 是空的，
表示提供全部已配置方式（含 Microsoft）；若是限定列表，就在那里勾上 **Microsoft** 再保存。

## 第 5 步 —— 决定谁能进来

开启 provider 决定的是**怎么登**，不是**谁能登**。准入规则没有变化：

| 设置 | 作用 |
|---|---|
| **Access mode**（后台 → 设置） | `open` / `invite` / `request` —— 管新平台成员 |
| `ADMIN_EMAILS` | 哪些邮箱成为平台管理员 |
| `ALLOWED_DOMAINS` | 邮箱域名白名单。单租户 Entra 配置下**跳过**（能进目录本身就是门禁）；tenant 为 `common` / `organizations` / `consumers` 时**强制生效** |
| 项目 **Auth** 标签页 | 该项目下游用户的允许域名与访问名单 |

:::warning 多租户默认是完全敞开的
tenant 填 `common` 或 `organizations` 时，全世界任何持微软工作账号的人都能走到你的登录页。
启用前先设好 `ALLOWED_DOMAINS`，或把 access mode 切成 `invite`。
:::

## 出问题时

| 现象 | 原因与处理 |
|---|---|
| `AADSTS50011` 重定向 URI 不匹配 | Azure 里的 URI 与 muvee 的不一致。从卡片复制而不是手打，必须完全一致（含协议和路径）。 |
| 保存后没有 Microsoft 按钮 | 凭据不全（tenant / client ID / 密码任一为空都会故意不注册），或 tenant 格式非法。查 muvee-server 日志里的 `entra provider`；项目页还要查该项目的 sign-in providers 列表。 |
| `id_token was issued by tenant …, not the configured …` | 应用注册是多租户，而 tenant 字段锁了单个 GUID。改成实际签发 token 的租户 GUID，或改成 `organizations`。 |
| 原来能登，突然全员登不上 | 客户端密码过期了。在 Azure 里新建一个，把新值粘回来。 |
| 用户没有头像 | 用户从没上传过头像时属正常（Graph 返回 404）。如果是**所有人**都没有，大概率缺 `User.Read` 授权，日志会打 `entra: fetch avatar`。两种情况都不影响登录。 |
| 在微软同意页面就失败 | 租户不给该 Graph scope 授权。取消勾选头像选项，muvee 就只请求 `openid profile email`。 |
