---
id: auth-wecom
title: 企业微信
sidebar_position: 3
---

# 企业微信

muvee 支持通过**企业微信扫码 SSO** 登录。用户使用企业微信手机端或桌面端扫码完成认证，无需输入密码。

## 前置条件

- 拥有管理员权限的**企业微信账号**，可登录 [work.weixin.qq.com](https://work.weixin.qq.com)
- 在企业下创建一个内部应用

## 配置步骤

### 1. 创建自建应用

1. 登录 [企业微信管理后台](https://work.weixin.qq.com/wework_admin/frame#apps)
2. 进入 **应用管理 → 创建应用 → 创建自建应用**
3. 填写应用名称、logo，选择可见范围（成员/部门）
4. 记录应用详情页中的 **AgentId**

### 2. 获取企业凭证

- **CorpId（企业ID）**：在 **我的企业 → 企业信息** 页面底部复制
- **App Secret（应用密钥）**：在应用详情页 → **API → Secret** 中生成并复制

### 3. 配置网页授权

在应用详情页进入 **网页授权及 JS-SDK** → 设置 **网页授权域名**，添加：
```
example.com
```

然后在 **OAuth 重定向 URI** 中添加：
```
https://example.com/auth/wecom/callback
```
将 `example.com` 替换为你的 `BASE_DOMAIN`。

### 4. 配置环境变量

```env
WECOM_CORP_ID=ww1234567890abcdef
WECOM_CORP_SECRET=your-app-secret
WECOM_AGENT_ID=1000001
# 可选：若不填，默认使用 http://localhost:8080/auth/wecom/callback
# WECOM_REDIRECT_URL=https://example.com/auth/wecom/callback
```

## 环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `WECOM_CORP_ID` | — | 企业 ID（CorpId） |
| `WECOM_CORP_SECRET` | — | 自建应用的 App Secret |
| `WECOM_AGENT_ID` | — | 自建应用的 AgentId |
| `WECOM_REDIRECT_URL` | `http://localhost:8080/auth/wecom/callback` | 应用中注册的回调地址 |

## 邮箱处理

企业微信成员档案包含两个邮箱字段。muvee 按以下优先级获取：

1. **企业邮箱**（`biz_mail`）—— 由管理员在企业微信后台配置的工作邮箱
2. **个人邮箱**（`email`）—— 员工绑定在企业微信账号上的个人邮箱
3. **合成邮箱** —— `{userid}@wecom.local`，无邮箱字段时使用

以 `*.local` 结尾的合成邮箱会**自动跳过** `ALLOWED_DOMAINS` 校验。企业微信应用的可见范围设置已限制了哪些成员可以认证。

:::note 非成员账号
企业微信扫码 SSO 仅对组织**内部成员**返回 `UserId`。外部联系人（非成员）不被支持，将收到错误提示。
:::

## 工作原理

用户扫码后，muvee 依次完成两次 API 调用：

1. **获取 UserId** —— 调用 `GET /cgi-bin/auth/getuserinfo?code=...`，将 OAuth code 解析为内部 `UserId`
2. **获取用户详情** —— 调用 `GET /cgi-bin/user/get?userid=...`，获取完整成员信息（姓名、头像、邮箱）

两次调用均使用通过 `WECOM_CORP_ID` + `WECOM_CORP_SECRET` 从 `GET /cgi-bin/gettoken` 获取的 **Corp Access Token**。
