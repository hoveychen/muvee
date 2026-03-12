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
| `JWT_SECRET` | `change-me-in-production` | Secret for signing JWT session tokens |

## ForwardAuth Service (`muvee-authservice`)

| Variable | Default | Description |
|---|---|---|
| `PORT` | `4181` | HTTP listen port |
| `GOOGLE_CLIENT_ID` | — | Same as control plane |
| `GOOGLE_CLIENT_SECRET` | — | Same as control plane |
| `FORWARD_AUTH_REDIRECT_URL` | `http://localhost:4181/_oauth` | OAuth2 callback URL for ForwardAuth |
| `JWT_SECRET` | — | Must match the control plane value |

## Agent (`muvee-agent`)

| Variable | Default | Description |
|---|---|---|
| `NODE_ROLE` | _(required)_ | `builder` or `deploy` |
| `CONTROL_PLANE_URL` | `http://localhost:8080` | Base URL of the control plane |
| `REGISTRY_ADDR` | `localhost:5000` | Docker registry address (builder nodes) |
| `DATA_DIR` | `/muvee/data` | Local dataset cache root (deploy nodes) |
| `BASE_DOMAIN` | `localhost` | Base domain for Traefik routing (deploy nodes) |
| `AUTH_SERVICE_URL` | — | ForwardAuth verify endpoint, e.g. `http://muvee-authservice:4181/verify` |
