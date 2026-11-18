---
id: forward-auth
title: ForwardAuth & Project Access Control
sidebar_position: 7
---

# ForwardAuth & Project Access Control

muvee supports per-project authentication for deployed applications. When enabled, any visitor to `{project}.domain.com` is first redirected to sign in via whichever OAuth provider you have configured (Google, Feishu, WeCom, DingTalk, etc.).

## How It Works

```
Browser → Traefik → ForwardAuth sidecar → OAuth provider
                         ↓
                    Validates JWT session cookie
                         ↓
                    Checks email domain against
                    project.auth_allowed_domains
                         ↓
                    200 OK → Traefik forwards request
                    403 → access denied
```

## Enabling Auth on a Project

In the **Project Detail → Auth** tab:

1. Toggle **Require Auth** to `Enabled`
2. Optionally set **Allowed Email Domains** (comma-separated, e.g. `company.com,partner.org`)
3. Optionally set **Auth Bypass Paths** — one path per line. Requests matching these paths will skip authentication. Use `*` suffix for prefix matching (e.g. `/api/public/*`).
4. Save and redeploy

When the container starts, the deploy agent attaches these Traefik labels:

```
traefik.http.middlewares.{proj}-auth.forwardauth.address=http://muvee-authservice:4181/verify?project={id}&domains=company.com
traefik.http.middlewares.{proj}-auth.forwardauth.authResponseHeaders=X-Forwarded-User,X-Forwarded-User-Name,X-Forwarded-User-Avatar,X-Forwarded-User-Provider
traefik.http.routers.{proj}.middlewares={proj}-auth
```

## Session Flow

Each project subdomain keeps its **own** session. There is no longer a cookie shared across `*.BASE_DOMAIN`: a session obtained on `a.BASE_DOMAIN` is useless on `b.BASE_DOMAIN`, so one deployed app can never replay a visitor's session against another.

1. User visits `{project}.domain.com`
2. Traefik calls ForwardAuth sidecar with the request headers
3. Sidecar checks the subsite session cookie `__Host-muvee_fwd` (JWT, host-only on that subdomain and bound to that project)
4. If missing/expired: a page load is redirected (302) to `{project}.domain.com/_oauth/login`. That page first asks the **central sign-in host** (the apex, i.e. the host of `FORWARD_AUTH_BASE_URL`) for a silent sign-in (`/_oauth/authorize?prompt=none`):
   - If the apex already holds a valid SSO session (`__Host-muvee_sso`), it issues a single-use code valid for 60 seconds, the subdomain exchanges it for its own `__Host-muvee_fwd`, and the user lands back on the original URL **without ever seeing a login page**.
   - Otherwise the login page renders on the subdomain (project branding, the project's provider list, password / SMS forms). Clicking a provider goes through the apex `/_oauth/start` to the provider; the callback still lands on the apex `/_oauth/<provider>`, which signs the SSO session and sends the code back to the subdomain.
5. Visiting a second project later repeats steps 1–4; with an SSO session at the apex it completes silently, provided the second project has enabled the provider the SSO session was signed in with. Otherwise that project's own sign-in flow runs (its login page, or a direct jump to its only provider).
6. On subsequent requests: validate JWT, check email domain, return `200`

Provider redirect URIs are unchanged (`https://BASE_DOMAIN/_oauth/<provider>`); no new URI has to be registered. The apex no longer serves a login page: `BASE_DOMAIN/_oauth/login` returns `400`, because sign-in always starts from the site being visited. If a code exchange fails (for example an expired code) the login page retries; after 2 attempts it shows an error page instead of looping. Sign-ins in progress when this behaviour was released are invalidated; the user just clicks sign in again.

**Sign-in state.** Each sign-in keeps its own short-lived state cookie on the subdomain (`__Host-fwd_state_<hash>`) and on the apex (`__Host-fwd_apex_state_<hash>`), named after that sign-in's state, so several sign-ins started from one browser (two tabs, a prefetched duplicate) never overwrite each other. A sign-in may take up to **60 minutes** on the provider's page. If the callback reaches the apex with a state it cannot match (expired, already used, from another browser), the provider's code is discarded and the browser is sent back to the login entry of the site it last started a sign-in for (`__Host-fwd_login_hint` on the apex, re-validated as a muvee site of the same base domain) to start over — usually a quick bounce, since the provider is still signed in. This happens at most twice within 10 minutes; otherwise, or when the apex does not know the site, a page asks the user to go back to the site and sign in again.

**Upgrade impact.** Session tokens must now carry the project they were issued for (`aud`), so when this release is deployed every existing subsite session is rejected on its next request. Each user goes through the provider once on their first visit to any project; after that the other projects sign in silently from the apex SSO. Device-flow (CLI) tokens issued before this release have no `aud` and are rejected immediately too; CLI users must run the device flow again against the project's host.

| Credential | Cookie (production) | Lifetime |
|------------|---------------------|----------|
| Subsite session | `__Host-muvee_fwd` on each project host | OAuth sign-in: 12 hours, sliding (re-issued once less than 6 hours remain). Password / SMS sign-in: fixed 7 days, never renewed |
| Central SSO session | `__Host-muvee_sso` on the apex only | 7 days, sliding (renewed at most once a day; the original sign-in time is kept) |

`__Host-` cookies are enforced by the browser to be host-only (no `Domain`), `Secure` and `Path=/`. The SSO cookie is never sent to a project. Cookies from earlier releases (`muvee_fwd_session` and friends, set on the base domain) are no longer written and are deleted on sign-out, on a rejected session and when a sign-in completes. On a plain-`http` development platform (`FORWARD_AUTH_BASE_URL` starting with `http://` and `FWD_INSECURE_DEV_COOKIES=1`) the cookies fall back to their names without the `__Host-` prefix; this is not usable in production.

A user who keeps coming back stays signed in through the sliding windows; after a subsite session expires it is silently renewed from the apex SSO while that is still valid, and only when the SSO has also expired does the user see the login page. Demo-account (password) and SMS sessions are the exception: they sign the subdomain in directly and never create an SSO session (so they do not sign the user into other projects). They keep a fixed 7-day lifetime, because nothing re-checks the account or phone grant behind them on each request.

Invitation links work as before: the invite token travels with the return URL and is consumed at verification time, not through a cookie.

### Sign-out

`GET {project}.domain.com/_oauth/logout` (optional `?redirect=`, limited to a path on the same site or an `https` URL on a platform domain; anything else lands on `/_oauth/logged-out`) clears that site's cookies, revokes the session **and the SSO session it was issued from**, and sets a "signed out" hint. Every other project session issued from the same SSO then stops working on its next request and shows the signed-out page, so signing out of one project signs the user out of all of them on that base domain. It does not sign the user out of Google or any other identity provider. While the hint cookie is present the login page shows the signed-out page rather than silently signing the user back in.

If the request carries `Sec-Fetch-Site` and it is not `same-origin` (for example a sign-out link on another project's page or an external site), a confirmation page is shown first and the user has to click to confirm. Typing the URL in the address bar or opening a bookmark (`Sec-Fetch-Site: none`) also shows the confirmation page; a sign-out link on a page of the same project does not. Signing out of the console still invalidates all of that user's project sessions.

### Custom domains and aliases

Sign-in is only supported on hosts under a platform base domain. A custom domain outside every base domain cannot sign in through ForwardAuth, and a base domain's apex cannot be registered as a project alias (the server rejects it).

When a session is missing or expired, a page load (step 4) is redirected to the
login page, but a `fetch` / XHR call from page script gets
`401 Unauthorized` with an `X-Muvee-Auth: login-required` header and a JSON body
`{"error":"login_required","login_url":"…"}`. Following a redirect to an HTML
login page would leave an SPA parsing HTML as JSON; the 401 is something it can
act on. Every project page already handles it through the injected
[auth-guard](./embed-bridge#auth-guard), which reloads the page so the user
goes through login and lands back where they were.

:::info How the OAuth callback is routed
The `/_oauth` path on `BASE_DOMAIN` is routed by Traefik directly to `muvee-authservice` (not to the main web UI). The apex `/_oauth` is now taken over by a high-priority central route generated by `muvee-server`, which clears any `X-Muvee-Aud`; the Traefik labels on the `muvee-authservice` container in `docker-compose.yml` can stay. Because Traefik gives higher priority to the more specific `Host + Path` rule, `BASE_DOMAIN/_oauth` is correctly handled by the auth sidecar while all other `BASE_DOMAIN` paths continue to reach `muvee-server`. `FORWARD_AUTH_BASE_URL` must be the base domain's apex, because the server only generates the central `/_oauth` route for the apex.
:::

## Auth Bypass Paths

When auth is enabled on a project you can exempt specific paths from authentication. This is useful for health checks, public APIs, or webhook endpoints that need to be reachable without a session.

Configure bypass paths in the **Auth** tab (one path per line) or via the CLI:

```bash
muveectl projects update PROJECT_ID --auth-bypass-paths "/health
/api/public/*"
```

| Pattern | Matches |
|---------|---------|
| `/health` | Exact path `/health` only |
| `/api/public/*` | Any path starting with `/api/public/` |

Each bypass path creates a higher-priority Traefik router that routes directly to the service without the ForwardAuth middleware.

## Traefik Dashboard

The Traefik dashboard (`traefik.BASE_DOMAIN`) is treated as one more subsite with its own independent session. The `docker-compose.yml` / `docker-compose.server.yml` templates route `traefik.BASE_DOMAIN/_oauth` to `muvee-authservice` and inject `X-Muvee-Aud=host:traefik.BASE_DOMAIN`. For project subdomains `muvee-server` injects the trusted `X-Muvee-Aud` itself and overwrites any client-supplied value; on the central host the header is stripped.

## Public Projects

If **Require Auth** is disabled on a project, no ForwardAuth middleware is attached — the project is publicly accessible.

## Per-Project Access Control (`access_mode`)

When **Require Auth** is enabled, every project also has an `access_mode` that decides which signed-in users are actually allowed to reach the deployed service. It is independent from the email-domain check above and runs after it.

| `access_mode` | Who can reach the service |
|---------------|---------------------------|
| `public` (default) | Any signed-in muvee user. |
| `private` | The project owner, system admins, and users explicitly listed in the project's allow-list. |

The decision is made by `muvee-server`'s `/api/internal/access/check` endpoint (called by the auth sidecar on every request) and recorded in the project's `Users` tab.

### Managing access from the UI

Open the project's **Users** tab to manage all of this in one place:

- **Service access** — toggle between Public and Private. Owners and system admins are always allowed regardless of mode and never need to be added to the allow-list.
- **Allowed users** — the explicit allow-list consulted when access is Private. Users have to sign in to muvee at least once before they can be invited (the lookup is by email).
- **Recent visitors** — unique users who have actually reached the deployed service, with a per-user visit count and last-seen timestamp. Useful for both modes:
  - For **Public** projects: see who's actually using your service.
  - For **Private** projects: each row gets a one-click **Allow** button that adds that user to the allow-list without having to re-type their email.
- **Pending requests** — when a denied user submits an access request (see below), it appears here with the reason they gave. Approve to add them to the allow-list immediately; deny to reject.

The sidebar **Projects** link shows a small badge with the total number of pending access requests across every project you own. It refreshes every minute.

### Request-access flow for denied users

When a user is denied by a Private project, the auth sidecar redirects them (HTTP 302) to `https://BASE_DOMAIN/request-access?project={id}` instead of returning a bare 403. There they:

1. Sign in to muvee if they haven't already.
2. See the project they were trying to reach.
3. Optionally enter a short reason (≤ 1000 chars).
4. Submit. The request lands in the project's **Users → Pending requests** panel.
5. Once the owner approves, the next visit succeeds without further prompting.

If the same user submits multiple times before a decision, the existing pending row is reused (not duplicated). After a decision the row stays around for history; a denied user can re-request later by submitting again. Approval is permanent in the sense that the user is added to `project_access_users` immediately; revoking access requires the owner to remove them from **Allowed users**.

### How visit recording works

Visit counts are written through an in-memory batch worker, not on the request hot path:

- Every successful access check pushes one event onto a 1024-slot channel (non-blocking — events are dropped with a warn log if the buffer ever saturates).
- A background goroutine flushes the channel every 5 seconds, or whenever 200 events have accumulated, into a single multi-row UPSERT against `project_visits`.
- Same-user events that arrive in the same batch are deduplicated; their counts are summed and `last_seen_at` is set to `GREATEST(existing, new)` so out-of-order arrivals never move the timestamp backwards.
- On graceful shutdown the worker drains the channel and does one final flush. A hard kill can lose up to ≤ 5 seconds of visit counters.

This keeps the ForwardAuth path one DB round-trip (the access check itself) instead of two.

## For Service Developers

If you are developing a service deployed on muvee and want to know how to read user identity, implement logout, or support CLI access, see the [Service Auth Integration](./service-auth-integration) guide.
