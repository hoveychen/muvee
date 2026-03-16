---
id: auth-feishu
title: Feishu / Lark
sidebar_position: 2
---

# Feishu / Lark

muvee supports sign-in via **Feishu** (飞书, mainland China) and its international counterpart **Lark** using the Feishu OAuth2 flow.

## Prerequisites

- A Feishu / Lark developer account at [open.feishu.cn](https://open.feishu.cn) (Feishu) or [open.larksuite.com](https://open.larksuite.com) (Lark)
- An internal app created in the developer console

## Setup

### 1. Create a Feishu app

1. Go to [open.feishu.cn/app](https://open.feishu.cn/app) and click **Create App → Internal App**
2. Fill in the app name and description

### 2. Add OAuth2 scopes

Navigate to **Permissions & Scopes** and enable:

| Scope | Purpose |
|---|---|
| `contact:user.email:readonly` | Read the user's enterprise email address |

:::tip
Without this scope, muvee falls back to a synthetic email (`{open_id}@feishu.local`). See [Email Handling](#email-handling) below.
:::

### 3. Configure the redirect URI

Go to **Security Settings** and add the following to **Redirect URLs**:
```
https://example.com/auth/feishu/callback
```
Replace `example.com` with your `BASE_DOMAIN`.

### 4. Configure environment variables

```env
FEISHU_APP_ID=cli_xxxxxxxxxxxx
FEISHU_APP_SECRET=your-app-secret
# Optional: defaults to https://example.com/auth/feishu/callback derived from BASE_DOMAIN
# FEISHU_REDIRECT_URL=https://example.com/auth/feishu/callback

# For international Lark (outside China), override the base URL:
# FEISHU_BASE_URL=https://open.larksuite.com
```

### 5. Publish the app

Go to **App Release → Version Management** and publish the app to make it available to all members of your organisation.

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `FEISHU_APP_ID` | — | App ID (`cli_...`) from the developer console |
| `FEISHU_APP_SECRET` | — | App Secret from the developer console |
| `FEISHU_REDIRECT_URL` | `http://localhost:8080/auth/feishu/callback` | Callback URL registered in the app |
| `FEISHU_BASE_URL` | `https://open.feishu.cn` | Base API URL. Set to `https://open.larksuite.com` for the international Lark edition |

## Email Handling

Feishu accounts may or may not have an associated email address. muvee resolves the user's identity in this priority order:

1. **Enterprise email** (`enterprise_email`) — the work email managed by the organisation
2. **Personal email** (`email`) — the user's personal email bound to the account
3. **Synthetic email** — `{open_id}@feishu.local` — used when no email is available

Synthetic `*.local` addresses **bypass** the `ALLOWED_DOMAINS` check. This is intentional: the Feishu app is already scoped to your organisation, so anyone who can authenticate through it is implicitly an authorised member.

If you want to restrict access further, use `ADMIN_EMAILS` to control who gets the `admin` role.
