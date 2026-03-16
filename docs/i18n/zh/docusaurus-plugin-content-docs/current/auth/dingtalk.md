---
id: auth-dingtalk
title: 钉钉
sidebar_position: 4
---

# 钉钉

muvee 支持通过**钉钉**的标准 OAuth2 授权码流程登录。用户跳转至钉钉登录页，使用钉钉账号完成认证后回到 muvee 控制台。

## 前置条件

- 在 [open.dingtalk.com](https://open.dingtalk.com) 注册开发者账号
- 在钉钉开放平台创建一个应用

## 配置步骤

### 1. 创建应用

1. 登录 [open.dingtalk.com](https://open.dingtalk.com)，进入 **应用开发 → 企业内部应用 → H5 微应用**（或其他支持 OAuth2 的应用类型）
2. 填写应用名称和描述
3. 记录 **AppKey**（即 `DINGTALK_CLIENT_ID`）和 **AppSecret**

### 2. 开启「登录钉钉」功能

1. 在应用详情页，进入 **安全设置 → 登录与分享**
2. 开启 **登录钉钉** 功能
3. 在 **回调域名** 中添加：
   ```
   https://example.com/auth/dingtalk/callback
   ```
   将 `example.com` 替换为你的 `BASE_DOMAIN`。

### 3. 配置 OAuth2 权限

在 **权限管理** 中，申请以下权限：

| 权限 | 用途 |
|---|---|
| `openid` | 获取用户的唯一钉钉身份标识 |
| `Contact.User.Read` | 读取用户基础信息（姓名、头像） |
| `Contact.User.mobile` | （可选）读取手机号 |

### 4. 配置环境变量

```env
DINGTALK_CLIENT_ID=ding1234567890abcdef
DINGTALK_CLIENT_SECRET=your-app-secret
# 可选：若不填，默认使用 http://localhost:8080/auth/dingtalk/callback
# DINGTALK_REDIRECT_URL=https://example.com/auth/dingtalk/callback
```

## 环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `DINGTALK_CLIENT_ID` | — | 开发者后台的 AppKey |
| `DINGTALK_CLIENT_SECRET` | — | 开发者后台的 AppSecret |
| `DINGTALK_REDIRECT_URL` | `http://localhost:8080/auth/dingtalk/callback` | 应用中注册的回调地址 |

## 邮箱处理

钉钉用户档案中的邮箱字段为可选。muvee 按以下优先级获取：

1. **邮箱**（`email`）—— 用户在钉钉档案中填写的邮箱地址
2. **合成邮箱** —— `{unionId}@dingtalk.local`，无邮箱时使用

以 `*.local` 结尾的合成邮箱会**自动跳过** `ALLOWED_DOMAINS` 校验。钉钉企业内部应用的访问控制已确保只有组织成员才能认证。

## 工作原理

1. 用户点击 **使用钉钉继续** → 跳转至 `login.dingtalk.com/oauth2/auth`
2. 用户认证后，钉钉将授权码返回至 `/auth/dingtalk/callback`
3. muvee 调用 `POST https://api.dingtalk.com/v1.0/oauth2/userAccessToken` 换取 Access Token
4. muvee 通过 `GET https://api.dingtalk.com/v1.0/contact/users/me`（携带 `x-acs-dingtalk-access-token` 请求头）获取用户信息
5. 在数据库中 upsert 用户，并签发有效期 7 天的 JWT 会话 Cookie
