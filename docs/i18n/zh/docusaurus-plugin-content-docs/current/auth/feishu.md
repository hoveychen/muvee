---
id: auth-feishu
title: 飞书 / Lark
sidebar_position: 2
---

# 飞书 / Lark

muvee 支持通过 **飞书**（mainland China）或国际版 **Lark** 的 OAuth2 流程登录。

## 前置条件

- 在 [open.feishu.cn](https://open.feishu.cn)（飞书）或 [open.larksuite.com](https://open.larksuite.com)（Lark）上创建开发者账号
- 在开发者后台创建一个**企业自建应用**

## 配置步骤

### 1. 创建自建应用

1. 打开 [open.feishu.cn/app](https://open.feishu.cn/app)，点击 **创建企业自建应用**
2. 填写应用名称和描述

### 2. 添加 OAuth2 权限

进入 **权限管理** 页面，开启以下权限：

| 权限 | 用途 |
|---|---|
| `contact:user.email:readonly` | 读取用户的企业邮箱地址 |

:::tip
若未开启该权限，muvee 将使用合成邮箱（`{open_id}@feishu.local`）。详见下方[邮箱处理](#邮箱处理)。
:::

### 3. 配置重定向 URI

进入 **安全设置**，在 **重定向 URL** 中添加：
```
https://example.com/auth/feishu/callback
```
将 `example.com` 替换为你的 `BASE_DOMAIN`。

### 4. 配置环境变量

```env
FEISHU_APP_ID=cli_xxxxxxxxxxxx
FEISHU_APP_SECRET=your-app-secret
# 可选：若不填，默认使用 http://localhost:8080/auth/feishu/callback
# FEISHU_REDIRECT_URL=https://example.com/auth/feishu/callback

# 国际版 Lark 用户请设置（中国大陆飞书无需设置）：
# FEISHU_BASE_URL=https://open.larksuite.com
```

### 5. 发布应用

进入 **版本管理与发布**，创建版本并发布，使组织内所有成员都能使用该应用。

## 环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `FEISHU_APP_ID` | — | 开发者后台的 App ID（`cli_...`） |
| `FEISHU_APP_SECRET` | — | 开发者后台的 App Secret |
| `FEISHU_REDIRECT_URL` | `http://localhost:8080/auth/feishu/callback` | 应用中注册的回调地址 |
| `FEISHU_BASE_URL` | `https://open.feishu.cn` | API 基础地址。国际版 Lark 设置为 `https://open.larksuite.com` |

## 邮箱处理

飞书账号不一定绑定了邮箱。muvee 按以下优先级获取用户邮箱：

1. **企业邮箱**（`enterprise_email`）—— 由组织管理员分配的工作邮箱
2. **个人邮箱**（`email`）—— 用户绑定在飞书账号上的个人邮箱
3. **合成邮箱** —— `{open_id}@feishu.local`，无邮箱时使用

以 `*.local` 结尾的合成邮箱会**自动跳过** `ALLOWED_DOMAINS` 校验。这是有意为之：飞书应用本身已限制只有组织内成员才能登录，无需再做二次域名校验。

如需进一步限制访问权限，可使用 `ADMIN_EMAILS` 控制哪些用户获得 `admin` 角色。
