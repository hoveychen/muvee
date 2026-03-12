---
id: traefik
title: Traefik Routing
sidebar_position: 9
---

# Traefik Routing

muvee uses [Traefik v3](https://doc.traefik.io/traefik/) as the reverse proxy. Traefik automatically discovers running containers via Docker labels.

## Automatic HTTPS

Traefik handles Let's Encrypt certificate provisioning. Set `ACME_EMAIL` in `.env` and ensure ports `80` and `443` are reachable.

## How Project Routing Works

When the deploy agent starts a container, it attaches labels like:

```
traefik.enable=true
traefik.http.routers.myapp.rule=Host(`myapp.example.com`)
traefik.http.routers.myapp.entrypoints=websecure
traefik.http.routers.myapp.tls.certresolver=letsencrypt
traefik.http.routers.myapp-http.rule=Host(`myapp.example.com`)
traefik.http.routers.myapp-http.entrypoints=web
traefik.http.routers.myapp-http.middlewares=redirect-to-https
```

Traefik picks these up within seconds — no manual config or reload required.

## ForwardAuth Labels (auth-required projects)

```
traefik.http.middlewares.myapp-auth.forwardauth.address=http://muvee-authservice:4181/verify?project=...
traefik.http.middlewares.myapp-auth.forwardauth.authResponseHeaders=X-Forwarded-User
traefik.http.routers.myapp.middlewares=myapp-auth
```

## Traefik Dashboard

Available at `:8081` when using `docker-compose.yml`. For production, restrict access to the dashboard.
