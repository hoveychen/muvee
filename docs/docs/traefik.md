---
id: traefik
title: Traefik Routing
sidebar_position: 9
---

# Traefik Routing

muvee uses [Traefik v3](https://doc.traefik.io/traefik/) as the reverse proxy.

## Automatic HTTPS

Traefik handles Let's Encrypt certificate provisioning. Set `ACME_EMAIL` in `.env` and ensure ports `80` and `443` are reachable.

## How Project Routing Works

muvee uses Traefik's **HTTP provider** to dynamically configure routes for deployed containers. This approach supports deploy nodes on separate machines from the control plane.

### Flow

1. The deploy agent starts the container with a **random host port** (`docker run -p 0:{container_port}`) on the deploy node.
2. The agent discovers the assigned port (`docker port`) and reports `host_ip:host_port` back to the control plane.
3. The control plane stores this endpoint in the database (`deployments.host_port`, `nodes.host_ip`).
4. Traefik polls `GET http://muvee-server:8080/api/traefik/config` every 5 seconds and receives a dynamically generated JSON config covering all running deployments.
5. Traefik immediately starts routing `{project}.BASE_DOMAIN` → `http://{node_ip}:{host_port}`.

### Generated Traefik Configuration (example)

```json
{
  "http": {
    "routers": {
      "myapp": {
        "rule": "Host(`myapp.example.com`)",
        "entryPoints": ["websecure"],
        "service": "myapp",
        "tls": { "certResolver": "letsencrypt" }
      },
      "myapp-http": {
        "rule": "Host(`myapp.example.com`)",
        "entryPoints": ["web"],
        "service": "myapp",
        "middlewares": ["redirect-to-https@file"]
      }
    },
    "services": {
      "myapp": {
        "loadBalancer": {
          "servers": [{ "url": "http://10.0.1.5:32768" }]
        }
      }
    }
  }
}
```

The `redirect-to-https` middleware is defined statically in `traefik/dynamic.yml` (file provider).

## ForwardAuth for Auth-Required Projects

When a project has **Auth Required** enabled, the HTTP provider config includes a per-project ForwardAuth middleware:

```json
"middlewares": {
  "myapp-auth": {
    "forwardAuth": {
      "address": "http://muvee-authservice:4181/verify?project=<id>",
      "authResponseHeaders": ["X-Forwarded-User"],
      "trustForwardHeader": true
    }
  }
}
```

The HTTPS router for that project references this middleware, enforcing Google sign-in before access.

## Container Port

Each project has a **Container Port** setting (default `8080`) — the port the application listens on inside the container. The deploy node maps this to a random host port. Your `Dockerfile` should `EXPOSE` (or simply listen on) the configured port.

## Traefik Providers

muvee uses three Traefik providers in parallel:

| Provider | Purpose |
|---|---|
| **HTTP** | Dynamically-deployed project containers (polled from muvee-server every 5s) |
| **Docker** | Control-plane services: muvee-server, registry, Traefik dashboard (discovered via Docker labels on the same host) |
| **File** | Static middlewares: `redirect-to-https`, `muvee-forward-auth`, `muvee-forward-auth-admin` |

## Traefik Dashboard

The dashboard is served at `https://traefik.BASE_DOMAIN` and protected by admin-only ForwardAuth. Only accounts listed in `ADMIN_EMAILS` can access it after signing in with Google.

Direct port access (`:8081`) has been removed — the dashboard is only reachable through the HTTPS router.
