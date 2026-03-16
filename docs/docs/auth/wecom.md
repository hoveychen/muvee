---
id: auth-wecom
title: WeCom (企业微信)
sidebar_position: 3
---

# WeCom (企业微信)

muvee supports enterprise WeChat Work (**WeCom / 企业微信**) sign-in via the **QR-code SSO** flow. Users scan a QR code with the WeCom mobile app or the WeCom desktop client to log in — no password entry required.

## Prerequisites

- A WeCom **Enterprise Account** (企业账号) with administrator access at [work.weixin.qq.com](https://work.weixin.qq.com)
- An internal application created under the enterprise

## Setup

### 1. Create an internal app

1. Log in to the [WeCom Admin Console](https://work.weixin.qq.com/wework_admin/frame#apps)
2. Go to **Apps → Create App → Create Internal App**
3. Fill in name, logo, and choose the applicable members/departments
4. Note down the **AgentId** from the app detail page

### 2. Obtain Corp credentials

From the **My Company → Company Information** page, copy the **CorpId** (企业ID).

From the app detail page, go to **API → Secret** and generate or copy the **App Secret** (应用密钥).

### 3. Configure the redirect URI

In the app detail page, go to **Web Auth → Set Authorised Domain** and add your domain:
```
example.com
```
Then in **Web Auth → OAuth Redirect URI**, add:
```
https://example.com/auth/wecom/callback
```
Replace `example.com` with your `BASE_DOMAIN`.

### 4. Configure environment variables

```env
WECOM_CORP_ID=ww1234567890abcdef
WECOM_CORP_SECRET=your-app-secret
WECOM_AGENT_ID=1000001
# Optional: defaults to http://localhost:8080/auth/wecom/callback
# WECOM_REDIRECT_URL=https://example.com/auth/wecom/callback
```

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `WECOM_CORP_ID` | — | Enterprise CorpId (企业ID) |
| `WECOM_CORP_SECRET` | — | App Secret (应用密钥) for your internal app |
| `WECOM_AGENT_ID` | — | AgentId of your internal app |
| `WECOM_REDIRECT_URL` | `http://localhost:8080/auth/wecom/callback` | Callback URL registered in the app |

## Email Handling

WeCom member profiles have two email fields. muvee resolves in this priority order:

1. **Business email** (`biz_mail`) — the corporate email managed by the WeCom admin
2. **Personal email** (`email`) — the employee's personal email bound to their WeCom account
3. **Synthetic email** — `{userid}@wecom.local` — used when no email field is populated

Synthetic `*.local` addresses **bypass** the `ALLOWED_DOMAINS` check. WeCom's app-level access control (members/departments assigned to the app) already restricts who can authenticate.

:::note Non-member accounts
The WeCom QR-code SSO only returns an internal `UserId` for members of your organisation. External contacts (non-members) are not supported and will receive an error.
:::

## How it works

The WeCom authentication flow involves two separate API calls after the user scans the QR code:

1. **Get UserID** — calls `GET /cgi-bin/auth/getuserinfo?code=...` to resolve the OAuth code to an internal `UserId`
2. **Get User Detail** — calls `GET /cgi-bin/user/get?userid=...` to retrieve the full member profile (name, avatar, email)

Both calls use a **Corp Access Token** obtained from `GET /cgi-bin/gettoken` using your `WECOM_CORP_ID` + `WECOM_CORP_SECRET`.
