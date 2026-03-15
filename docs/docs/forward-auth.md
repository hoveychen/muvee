---
id: forward-auth
title: ForwardAuth & Project Access Control
sidebar_position: 7
---

# ForwardAuth & Project Access Control

muvee supports per-project Google authentication for deployed applications. When enabled, any visitor to `{project}.domain.com` is first redirected to Google sign-in.

## How It Works

```
Browser → Traefik → ForwardAuth sidecar → Google OAuth2
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

In the **Project Detail → Config** tab:

1. Toggle **Require Google Auth** to `Enabled`
2. Optionally set **Allowed Email Domains** (comma-separated, e.g. `company.com,partner.org`)
3. Save and redeploy

When the container starts, the deploy agent attaches these Traefik labels:

```
traefik.http.middlewares.{proj}-auth.forwardauth.address=http://muvee-authservice:4181/verify?project={id}&domains=company.com
traefik.http.middlewares.{proj}-auth.forwardauth.authResponseHeaders=X-Forwarded-User
traefik.http.routers.{proj}.middlewares={proj}-auth
```

## Session Flow

1. User visits `{project}.domain.com`
2. Traefik calls ForwardAuth sidecar with the request headers
3. Sidecar checks `muvee_fwd_session` cookie (JWT)
4. If missing/expired: redirect to Google OAuth2 (`/_oauth` callback on `BASE_DOMAIN`)
5. After Google login: set JWT cookie (domain-wide, shared across all `*.BASE_DOMAIN` subdomains), redirect back to original URL
6. On subsequent requests: validate JWT, check email domain, return `200`

:::info How the OAuth callback is routed
The `/_oauth` path on `BASE_DOMAIN` is routed by Traefik directly to `muvee-authservice` (not to the main web UI). This is configured via Traefik labels on the `muvee-authservice` container in `docker-compose.yml`. Because Traefik gives higher priority to the more specific `Host + Path` rule, `BASE_DOMAIN/_oauth` is correctly handled by the auth sidecar while all other `BASE_DOMAIN` paths continue to reach `muvee-server`.
:::

## Public Projects

If **Require Google Auth** is disabled on a project, no ForwardAuth middleware is attached — the project is publicly accessible.
