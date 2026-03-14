---
id: configuration
title: Configuration Reference
sidebar_position: 3
---

# Configuration Reference

All configuration is via environment variables.

## Control Plane (`muvee-server`)

| Variable | Default | Description |
|---|---|---|
| `DATABASE_URL` | `postgres://muvee:muvee@localhost:5432/muvee?sslmode=disable` | PostgreSQL connection string |
| `MIGRATIONS_DIR` | `./db/migrations` | Path to SQL migration files |
| `PORT` | `8080` | HTTP listen port |
| `BASE_DOMAIN` | `localhost` | Root domain; projects are served at `{prefix}.BASE_DOMAIN` |
| `GOOGLE_CLIENT_ID` | — | Google OAuth2 client ID |
| `GOOGLE_CLIENT_SECRET` | — | Google OAuth2 client secret |
| `GOOGLE_REDIRECT_URL` | `http://localhost:8080/auth/google/callback` | OAuth2 callback URL |
| `ALLOWED_DOMAINS` | _(allow all)_ | Comma-separated email domains allowed to sign in (e.g. `company.com`) |
| `ADMIN_EMAILS` | — | Comma-separated Google accounts that are auto-promoted to `admin` on login and can access `traefik.BASE_DOMAIN` |
| `JWT_SECRET` | `change-me-in-production` | Secret for signing JWT session tokens |
| `AGENT_SECRET` | — | Shared secret for agent ↔ server authentication (set the same value on all agents). If unset, agent endpoints are unauthenticated (dev only). |
| `AUTH_SERVICE_URL` | `http://muvee-authservice:4181` | Internal URL of `muvee-authservice`; used when generating per-project ForwardAuth config for Traefik |

## ForwardAuth Service (`muvee-authservice`)

| Variable | Default | Description |
|---|---|---|
| `PORT` | `4181` | HTTP listen port |
| `GOOGLE_CLIENT_ID` | — | Same as control plane |
| `GOOGLE_CLIENT_SECRET` | — | Same as control plane |
| `FORWARD_AUTH_REDIRECT_URL` | `http://localhost:4181/_oauth` | OAuth2 callback URL for ForwardAuth |
| `JWT_SECRET` | — | Must match the control plane value |
| `ADMIN_EMAILS` | — | Must match the control plane value; used to gate `/verify-admin` (Traefik dashboard) |

## Agent (`muvee-agent`)

| Variable | Default | Description |
|---|---|---|
| `NODE_ROLE` | _(required)_ | `builder` or `deploy` |
| `CONTROL_PLANE_URL` | `http://localhost:8080` | **Internal** address of the control plane (e.g. `http://10.0.0.1:8080`). Do not use the public domain — see [Agent Nodes](./agents) for details. |
| `AGENT_SECRET` | — | Must match the value set on the control plane |
| `REGISTRY_ADDR` | `localhost:5000` | Docker registry address (required on both builder and deploy nodes) |
| `REGISTRY_USER` | — | Registry Basic Auth username; agent runs `docker login` on startup |
| `REGISTRY_PASSWORD` | — | Registry Basic Auth password |
| `DATA_DIR` | `/muvee/data` | Local dataset cache root (deploy nodes) |
| `BASE_DOMAIN` | `localhost` | Base domain used for log messages (deploy nodes) |
| `HOST_IP` | _(auto-detect)_ | IP address Traefik uses to reach containers on this node. Auto-detected from the network interface used to reach `CONTROL_PLANE_URL`. Override if auto-detection selects the wrong interface (e.g. on multi-NIC hosts). |
