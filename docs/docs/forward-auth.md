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

1. User visits `{project}.domain.com`
2. Traefik calls ForwardAuth sidecar with the request headers
3. Sidecar checks `muvee_fwd_session` cookie (JWT)
4. If missing/expired: redirect to the configured OAuth provider (`/_oauth` callback on `BASE_DOMAIN`)
5. After login: set JWT cookie (domain-wide, shared across all `*.BASE_DOMAIN` subdomains), redirect back to original URL
6. On subsequent requests: validate JWT, check email domain, return `200`

:::info How the OAuth callback is routed
The `/_oauth` path on `BASE_DOMAIN` is routed by Traefik directly to `muvee-authservice` (not to the main web UI). This is configured via Traefik labels on the `muvee-authservice` container in `docker-compose.yml`. Because Traefik gives higher priority to the more specific `Host + Path` rule, `BASE_DOMAIN/_oauth` is correctly handled by the auth sidecar while all other `BASE_DOMAIN` paths continue to reach `muvee-server`.
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
