---
id: auth-dingtalk
title: DingTalk (钉钉)
sidebar_position: 4
---

# DingTalk (钉钉)

muvee supports sign-in via **DingTalk** (钉钉) using the standard OAuth2 authorization code flow. Users are redirected to the DingTalk login page and authenticate with their DingTalk credentials.

## Prerequisites

- A DingTalk developer account at [open.dingtalk.com](https://open.dingtalk.com)
- An app created in the DingTalk Open Platform

## Setup

### 1. Create an app

1. Go to [open.dingtalk.com](https://open.dingtalk.com) and log in with your DingTalk account
2. Click **Application Development → Internal Enterprise Application → H5 Micro Application** (or choose another type compatible with OAuth2)
3. Fill in the app name and description
4. Note down the **AppKey** (used as `DINGTALK_CLIENT_ID`) and **AppSecret**

### 2. Enable Login with DingTalk

1. In the app detail page, go to **Login with DingTalk** (登录钉钉)
2. Enable the feature
3. Under **Callback Domain / Redirect URI**, add:
   ```
   https://example.com/auth/dingtalk/callback
   ```
   Replace `example.com` with your `BASE_DOMAIN`.

### 3. Configure OAuth2 permissions

In **Permissions Management**, add the following scopes:

| Scope | Purpose |
|---|---|
| `openid` | Obtain the user's unique DingTalk identity |
| `Contact.User.Read` | Read basic user information (name, avatar) |
| `Contact.User.mobile` | (Optional) Read mobile number |

### 4. Configure environment variables

```env
DINGTALK_CLIENT_ID=ding1234567890abcdef
DINGTALK_CLIENT_SECRET=your-app-secret
# Optional: defaults to http://localhost:8080/auth/dingtalk/callback
# DINGTALK_REDIRECT_URL=https://example.com/auth/dingtalk/callback
```

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `DINGTALK_CLIENT_ID` | — | App Key (AppKey) from the developer console |
| `DINGTALK_CLIENT_SECRET` | — | App Secret from the developer console |
| `DINGTALK_REDIRECT_URL` | `http://localhost:8080/auth/dingtalk/callback` | Callback URL registered in the app |

## Email Handling

DingTalk user profiles include an optional `email` field. muvee resolves in this priority order:

1. **Email** (`email`) — the email address the user has registered in their DingTalk profile
2. **Synthetic email** — `{unionId}@dingtalk.local` — used when no email is available

Synthetic `*.local` addresses **bypass** the `ALLOWED_DOMAINS` check. DingTalk's internal app access control already ensures only organisation members can authenticate.

## How it works

1. User clicks **Continue with DingTalk** → redirected to `login.dingtalk.com/oauth2/auth`
2. After authentication, DingTalk returns an authorization code to `/auth/dingtalk/callback`
3. muvee exchanges the code for an access token via `POST https://api.dingtalk.com/v1.0/oauth2/userAccessToken`
4. muvee fetches the user profile via `GET https://api.dingtalk.com/v1.0/contact/users/me` using the `x-acs-dingtalk-access-token` header
5. The user is upserted in the database and a 7-day JWT session cookie is issued
