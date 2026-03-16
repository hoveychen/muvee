---
id: auth-google
title: Google OAuth2
sidebar_position: 1
---

# Google OAuth2

muvee supports Google sign-in via **OpenID Connect (OIDC)**. This is the simplest option for teams that already use Google Workspace or personal Google accounts.

## Prerequisites

- A [Google Cloud](https://console.cloud.google.com/) project with the **OAuth consent screen** configured
- The control plane accessible over HTTPS (required by Google's redirect URI policy)

## Setup

### 1. Create OAuth2 credentials

1. Go to **APIs & Services → Credentials** in [Google Cloud Console](https://console.cloud.google.com/apis/credentials)
2. Click **Create Credentials → OAuth client ID**
3. Select **Web application** as the application type
4. Under **Authorized redirect URIs**, add **both** of the following (replace `example.com` with your `BASE_DOMAIN`):
   ```
   https://example.com/auth/google/callback
   https://example.com/_oauth
   ```
   The first URI is for the control-panel login. The second is for the per-project **ForwardAuth** sidecar (`muvee-authservice`).
5. Click **Create** and copy the **Client ID** and **Client Secret**

### 2. Configure environment variables

```env
GOOGLE_CLIENT_ID=your-client-id.apps.googleusercontent.com
GOOGLE_CLIENT_SECRET=your-client-secret
# Optional: only needed if your callback URL differs from the default
# GOOGLE_REDIRECT_URL=https://example.com/auth/google/callback
```

### 3. (Optional) Restrict access by email domain

```env
# Only allow sign-in from these domains (comma-separated)
ALLOWED_DOMAINS=your-company.com,partner.com
```

Leave `ALLOWED_DOMAINS` empty to allow any Google account to sign in.

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `GOOGLE_CLIENT_ID` | — | OAuth2 client ID |
| `GOOGLE_CLIENT_SECRET` | — | OAuth2 client secret |
| `GOOGLE_REDIRECT_URL` | `http://localhost:8080/auth/google/callback` | Callback URL registered in Google Cloud Console |

## How it works

muvee uses the standard OIDC code flow:

1. User clicks **Continue with Google** → redirected to `accounts.google.com`
2. Google returns an authorization code to `/auth/google/callback`
3. muvee exchanges the code for an ID token and verifies it with Google's JWKS
4. The `email`, `name`, and `picture` claims are read from the verified ID token
5. The email is checked against `ALLOWED_DOMAINS` (if set), then the user is upserted in the database and a 7-day JWT session cookie is issued

:::note ForwardAuth
The `muvee-authservice` sidecar uses the same Google credentials with a separate redirect URL (`FORWARD_AUTH_REDIRECT_URL`). This lets Traefik protect project subdomains behind Google sign-in without touching the main control-panel session.
:::
