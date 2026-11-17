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

muvee's own session cookies (`muvee_fwd_session`, `muvee_invite_token`, `fwd_oauth_state`) are removed from the request before it reaches your service, so another project on the same base domain can never receive — and replay — a user's session. Your own cookies are forwarded unchanged. Use `/_oauth/*` from the browser (for example `/_oauth/userinfo`) rather than forwarding the session cookie from your backend.

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

The simplest sign-out needs no code: link to `/_oauth/logout` on your app's own host (Traefik routes `/_oauth/*` on every project host to the auth gateway):

```html
<a href="/_oauth/logout">Sign out</a>
```

This clears the `muvee_fwd_session` cookie and lands on a branded "You've signed out" page with a **Sign in again** button. It does not send the user back to `/`: for a project with a single OAuth provider that would immediately sign them back in through the provider's still-active session.

To send the user somewhere else after sign-out, pass `redirect`:

```
https://myapp.example.com/_oauth/logout?redirect=/goodbye
```

`redirect` must be a path on the same site (starting with `/`, not `//`) or an `https` URL on this platform's domains. Any other value is ignored and the user lands on the signed-out page.

Sign-out also leaves a `muvee_fwd_signed_out` marker cookie. Until the user signs in again, the login page shows the sign-in button instead of jumping straight to a single OAuth provider, so an old tab that reloads after sign-out (e.g. the auth-guard noticing the expired session) does not quietly sign the user back in.

The session cookie is shared by every project under the same base domain, so signing out of one project signs the user out of all of them. It does not sign the user out of the OAuth provider (e.g. Google) or of the muvee console. The reverse does apply: signing out of the muvee console also ends the user's project sessions on every device and every base domain — the next request to any project asks them to sign in again. Bearer tokens obtained through the device flow below are not affected (tokens issued before muvee v1.40.6 are, since they cannot be told apart from sessions).

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
