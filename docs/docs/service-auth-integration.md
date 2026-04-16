---
id: service-auth-integration
title: Service Auth Integration
sidebar_position: 8
---

# Service Auth Integration

This guide is for developers who deploy services on muvee and want to leverage muvee's built-in authentication. When a project has **Require Auth** enabled, muvee handles the entire OAuth login flow — your service receives authenticated requests with user identity already resolved.

:::note Authentication, not authorization
muvee tells your service **who the user is** (authentication). It does **not** manage what each user is allowed to do — that is authorization, and it is your service's responsibility. For example, muvee will tell you "this request is from `alice@company.com`", but your service decides whether Alice can access a given resource.
:::

## Reading User Identity (Server-Side)

Every authenticated request that reaches your service includes the following headers, injected by Traefik after muvee's auth gateway verifies the user:

| Header | Value | Always present |
|--------|-------|----------------|
| `X-Forwarded-User` | Email address (e.g. `alice@company.com`) | Yes |
| `X-Forwarded-User-Name` | Display name (e.g. `Alice Zhang`) | When available from the OAuth provider |
| `X-Forwarded-User-Avatar` | Avatar image URL | When available from the OAuth provider |
| `X-Forwarded-User-Provider` | OAuth provider name (e.g. `google`, `feishu`) | Yes |

These headers are set by Traefik, not by the end user — they **cannot be forged** as long as your service is only reachable through Traefik. Your service does not need to parse cookies, validate JWTs, or talk to any OAuth provider.

```python
# Python / Flask
email    = request.headers.get("X-Forwarded-User")
name     = request.headers.get("X-Forwarded-User-Name")
avatar   = request.headers.get("X-Forwarded-User-Avatar")
provider = request.headers.get("X-Forwarded-User-Provider")
```

```go
// Go
email    := r.Header.Get("X-Forwarded-User")
name     := r.Header.Get("X-Forwarded-User-Name")
avatar   := r.Header.Get("X-Forwarded-User-Avatar")
provider := r.Header.Get("X-Forwarded-User-Provider")
```

```javascript
// Node.js / Express
const email    = req.headers["x-forwarded-user"];
const name     = req.headers["x-forwarded-user-name"];
const avatar   = req.headers["x-forwarded-user-avatar"];
const provider = req.headers["x-forwarded-user-provider"];
```

## Reading User Identity (Client-Side)

Frontend JavaScript running in the browser cannot see these headers — they exist only between Traefik and your backend. To get user info from the frontend, call the auth gateway's userinfo endpoint:

```javascript
const resp = await fetch("https://{BASE_DOMAIN}/_oauth/userinfo", {
  credentials: "include",
});
if (resp.ok) {
  const { email, name, avatar_url, provider } = await resp.json();
}
```

Returns `401` if the user is not authenticated.

## Logout

To log the user out, redirect them to the auth gateway's logout endpoint:

```
https://{BASE_DOMAIN}/_oauth/logout?redirect=https://myapp.example.com
```

This clears the `muvee_fwd_session` cookie and redirects the user to the URL specified by `redirect` (defaults to `/` if omitted).

## CLI / Headless Access (Device Flow)

For CLI tools or headless environments that need to access your auth-protected service without a browser session, muvee provides a Device Flow similar to `gh auth login`.

### Step 1 — Request a device code

```bash
curl -X POST https://{BASE_DOMAIN}/_oauth/device/code
```

```json
{
  "device_code": "XXXXXXXXXXX...",
  "user_code": "ABCD-1234",
  "verification_uri": "https://{BASE_DOMAIN}/_oauth/device/activate",
  "verification_uri_complete": "https://{BASE_DOMAIN}/_oauth/device/activate?code=ABCD-1234",
  "expires_in": 600,
  "interval": 5
}
```

### Step 2 — Direct the user to authorize

Print a message with the complete URL so the user can click or copy-paste it directly:

```
Authorize this device: https://{BASE_DOMAIN}/_oauth/device/activate?code=ABCD-1234
```

Opening this URL takes the user to the OAuth login page. After login the browser shows a confirmation page, and the user can close the tab.

### Step 3 — Poll for the token

```bash
curl -X POST https://{BASE_DOMAIN}/_oauth/device/token \
  -H "Content-Type: application/json" \
  -d '{"device_code": "XXXXXXXXXXX..."}'
```

Poll every `interval` seconds. While the user hasn't authorized yet, the response is:

```json
{"error": "authorization_pending"}
```

Once authorized:

```json
{
  "access_token": "eyJhbGciOiJI...",
  "token_type": "Bearer",
  "expires_in": 7776000
}
```

### Step 4 — Use the token

Pass the token as a Bearer header on every request:

```bash
curl -H "Authorization: Bearer eyJhbGciOiJI..." https://myapp.example.com/api/data
```

The token is valid for 90 days and works with all auth-protected projects under the same `BASE_DOMAIN`.
