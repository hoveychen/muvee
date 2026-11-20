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

:::warning Give your users a way to sign out
muvee signs users in for you, but it cannot put a sign-out button on your pages. Every page of a project with **Require Auth** should link to the account page muvee serves on every project host:

```html
<a href="/_oauth/me">Account</a>
```

`/_oauth/me` shows who is signed in, with **Sign out** and **Switch account**. Linking straight to `/_oauth/logout` also works. If your app keeps its own session, also follow [Apps with their own session](#apps-with-their-own-session).
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

muvee's own auth cookies (the per-project session `__Host-muvee_fwd`, the central sign-in session `__Host-muvee_sso`, and the short-lived login helper cookies, plus the legacy names of earlier releases) are removed from the request before it reaches your service, so your service never receives a user's session. Your own cookies are forwarded unchanged. Use `/_oauth/*` from the browser (for example `/_oauth/userinfo`) rather than forwarding the session cookie from your backend.

## Sessions Are Per Project

Each project host gets its own session cookie, `__Host-muvee_fwd`. It is host-only (no `Domain`), so the browser never sends it to another subdomain, and the session itself is bound to that one project: **a session from one project is not valid on any other project**. In particular, a `fetch(..., { credentials: "include" })` from `a.example.com` to `b.example.com/_oauth/userinfo` no longer carries a usable session — call `/_oauth/*` on the project's own host instead.

Signing in is still seamless across projects. muvee keeps a separate central sign-in session (`__Host-muvee_sso`, 7 days, sliding) on the base domain's apex host, which is never sent to any project. When a user opens a second project, it silently asks the apex host for a one-time code and mints its own session from it — the user does not see a login page. An OAuth-based project session lasts 12 hours and renews while in use; a password / SMS session lasts 7 days and does not renew, and does not create a central session (so it does not sign the user in to other projects).

OAuth providers still call back on the apex host, so no new redirect URI needs to be registered. The apex host serves no login page of its own. Custom domains (aliases) outside the platform's base domains do not support sign-in, and a base domain's apex cannot be registered as a project alias.

**Upgrade impact.** Session tokens must now carry the project they were issued for, so on upgrade every existing project session stops working at once. Each user signs in through the provider once on their next visit to any project; after that, other projects sign them in without another prompt. Device-flow tokens issued before this release carry no such binding and are rejected immediately: CLI users must run the device flow again against the project's host.

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

Frontend JavaScript running in the browser cannot see these headers — they exist only between Traefik and your backend. To get user info from the frontend, call the auth gateway's userinfo endpoint on your app's own host (a relative path, so it is same-origin):

```javascript
const resp = await fetch("/_oauth/userinfo", {
  credentials: "include",
});
if (resp.ok) {
  const { email, name, avatar_url, provider } = await resp.json();
}
```

Returns `401` if the user is not authenticated.

In a browser you can also use the `@muvee/auth` SDK (0.2.0). `MuveeAuth.getUser()` wraps `/_oauth/userinfo` and `MuveeAuth.signOut()` wraps `/_oauth/logout`.

### Sign in from a pop-up (`@muvee/auth` 0.2.0)

`signIn(provider)` opens a pop-up that goes through the central sign-in host straight into the chosen provider (it does not show the project's login page) and returns a handle **synchronously**:

```javascript
const handle = auth.signIn("google");   // call from a click handler, before any await
const user = await handle.wait();       // resolves with the signed-in user
```

`handle` has `url` (the sign-in start URL; `oauthUrl` is a deprecated alias), `popup` (the window, or `null` if the browser blocked it — then let the user open `handle.url` from a link), `wait()` and `cancel()`. `cancel()` closes the pop-up and rejects `wait()` with `AuthError('cancelled')`. Options: `popup` (a window you already opened in the click handler) and `timeoutMs` (default 10 minutes; on timeout `wait()` rejects with `AuthError('expired')`). Once sign-in completes, the pop-up notifies your page and `wait()` resolves with `getUser()`'s result.

The session is a cookie set in that browser, so since 0.2.0 `signIn` is not supported in non-browser hosts such as Tauri, React Native or Electron; do not call it there. Only an environment with no global `window` at all throws `AuthError('unsupported')` (if `baseUrl` was never configured, `not_configured` is thrown first). `baseUrl` must be the same origin as the page. `minPollIntervalSec` is deprecated and ignored. Upgrading from 0.1.x: replace `await signIn(p)` with `const h = signIn(p); await h.wait()`; a 0.1.x `signIn` against current muvee resolves `wait()` with `undefined` and establishes no session.

The legacy `POST /_oauth/login-token` and `/poll` endpoints still exist, but a successful poll only returns `{"status":"success"}` — it never returns identity or credentials.

## Logout

The simplest sign-out entry needs no code: link to the account page `/_oauth/me` on your app's own host (Traefik routes `/_oauth/*` on every project host to the auth gateway):

```html
<a href="/_oauth/me">Account</a>
```

The account page shows the account signed in to this project and the sign-in method, with three buttons: **Sign out** (`/_oauth/logout`), **Switch account** (signs out, then shows the sign-in page without silently signing back in through the platform's central session) and **Muvee console**. When nobody is signed in it shows a sign-in link instead. It is never cached and cannot be framed by another site.

To sign out in one click, link to `/_oauth/logout` directly:

```html
<a href="/_oauth/logout">Sign out</a>
```

This signs the user out of this project and ends their central sign-in session on the base domain, then lands on a branded "You've signed out" page with a **Sign in again** button. It does not send the user back to `/`: for a project with a single OAuth provider that would immediately sign them back in through the provider's still-active session.

To send the user somewhere else after sign-out, pass `redirect`:

```
https://myapp.example.com/_oauth/logout?redirect=/goodbye
```

`redirect` must be a path on the same site (starting with `/`, not `//`) or an `https` URL on this platform's domains; values containing backslashes or control characters are also rejected. Any other value is ignored and the user lands on the signed-out page.

Sign-out also leaves a `__Host-muvee_fwd_signed_out` marker cookie (host-only, on this project). Until the user signs in again, the login page shows the sign-in button instead of jumping straight to a single OAuth provider, so an old tab that reloads after sign-out (e.g. the auth-guard noticing the expired session) does not quietly sign the user back in.

Sign-out revokes this project's session and the central session it was minted from, so every other project's session minted from the same central session stops working on its next request (it shows the signed-out page): signing out of one project still signs the user out of all projects on that base domain. It does not sign the user out of the OAuth provider (e.g. Google) or of the muvee console. The reverse does apply: signing out of the muvee console also ends the user's project sessions on every device and every base domain — the next request to any project asks them to sign in again. Signing out of the console also revokes every Bearer token the user obtained through the device flow below, on every project; see [Token revocation](#token-revocation) for how to get a new one.

If `/_oauth/logout` is opened from another origin — for example a link or image embedded in another page, detected through the `Sec-Fetch-Site` header — muvee first shows a confirmation page and only signs the user out when they click confirm. Requests without that header (curl, old browsers) sign out directly.

Users can also sign out of every project at once from the muvee console: **Profile → Subsite sign-ins → Sign out of all subsites** ends every project session of that console account on every device and base domain (the console session and CLI tokens stay). It only covers the account signed in to the console; projects the user signed in to with another email account are not affected.

## Apps with their own session

If your service keeps its own login session (a session cookie, a server-side session store) on top of the identity muvee passes in, two extra rules apply.

**1. Sign out in order.** Your sign-out endpoint clears your own session first, then redirects to `/_oauth/logout`. Redirecting to `/_oauth/logout` alone is not enough: the muvee session ends, but your app's cookie stays in the browser.

```js
app.get('/logout', (req, res) => {
  req.session.destroy();               // clear your own session first
  res.clearCookie('app_sid');
  res.redirect('/_oauth/logout');      // then let muvee end the platform session
});
```

**2. Compare the identity on every request.** Most sign-outs never reach your `/logout`: the user signs out of the console, signs out on another project, uses *Sign out of all subsites*, or signs in again with a different account (a Google and a Feishu account are different users even with the same name). Your old session cookie survives all of these, and the next request that passes ForwardAuth may carry a different `X-Forwarded-User`. Check it on every request and drop the old session when it changed — otherwise the new user is served the previous user's session:

```js
app.use((req, res, next) => {
  const user = req.headers['x-forwarded-user'];
  if (req.session.user && req.session.user !== user) {
    req.session.destroy();             // identity changed: drop the old session
    return res.redirect(req.originalUrl);
  }
  next();
});
```

## CLI / Headless Access (Device Flow)

For CLI tools or headless environments that need to access your auth-protected service without a browser session, muvee provides a Device Flow similar to `gh auth login`. Run the flow against the project's own host (shown below as `myapp.example.com`): the resulting token is bound to that project.

### Step 1 — Request a device code

```bash
curl -X POST https://myapp.example.com/_oauth/device/code
```

```json
{
  "device_code": "XXXXXXXXXXX...",
  "user_code": "ABCD-1234",
  "verification_uri": "https://myapp.example.com/_oauth/device/activate",
  "verification_uri_complete": "https://myapp.example.com/_oauth/device/activate?code=ABCD-1234",
  "expires_in": 600,
  "interval": 5
}
```

### Step 2 — Direct the user to authorize

Print a message with the complete URL so the user can click or copy-paste it directly:

```
Authorize this device: https://myapp.example.com/_oauth/device/activate?code=ABCD-1234
```

Opening this URL shows a device login page that names the host the CLI will get access to; the user has to click a provider button to confirm and continue to the OAuth login. After login the browser shows a confirmation page, and the user can close the tab.

### Step 3 — Poll for the token

```bash
curl -X POST https://myapp.example.com/_oauth/device/token \
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

The token is valid for 90 days and works only for the project whose host started the device flow; it is rejected by other projects.

### Token revocation

A device-flow token stops working before its 90 days are up when its user

- signs out of the muvee console, or
- clicks **Revoke all CLI tokens** on the console's token settings page (this keeps the user signed in to the console and to project sites in the browser).

Either one revokes every device-flow token issued to that user up to that moment, on every project; tokens obtained afterwards work normally. Personal and project API tokens used with `muveectl` are not affected.

A revoked token is answered with `401 Unauthorized`, a `WWW-Authenticate: Bearer error="invalid_token"` header and a JSON body naming the device-flow endpoint of the host it was sent to — never with a redirect to the login page:

```json
{
  "error": "token_revoked",
  "message": "This CLI token has been revoked (console sign-out or \"revoke all CLI tokens\"). Run the device authorization flow again against https://myapp.example.com/_oauth/device/code to get a new token.",
  "device_code_url": "https://myapp.example.com/_oauth/device/code"
}
```

To get a new token, run the device flow again from [Step 1](#step-1--request-a-device-code) against the same host. A CLI should treat `error: "token_revoked"` (or any 401 on a Bearer request) as "sign in again", discard the stored token and restart the flow.

Tokens issued before this feature shipped keep working until their user's next console sign-out or click on the revoke button; an earlier console sign-out does not count.
