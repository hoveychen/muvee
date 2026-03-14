---
id: traefik
title: Traefik 路由
sidebar_position: 9
---

# Traefik 路由

muvee 使用 [Traefik v3](https://doc.traefik.io/traefik/) 作为反向代理。

## 自动 HTTPS

Traefik 负责 Let's Encrypt 证书的申请和续期。在 `.env` 中设置 `ACME_EMAIL`，并确保 `80` 和 `443` 端口可从外网访问。

## 项目路由原理

muvee 使用 Traefik 的 **HTTP provider** 动态配置已部署容器的路由。这种方式支持部署节点与控制平面部署在不同的机器上。

### 流程

1. 部署 Agent 在部署节点上以**随机主机端口**启动容器（`docker run -p 0:{container_port}`）。
2. Agent 发现已分配的端口（`docker port`），并将 `host_ip:host_port` 报告给控制平面。
3. 控制平面将该端点存入数据库（`deployments.host_port`、`nodes.host_ip`）。
4. Traefik 每 5 秒轮询一次 `GET http://muvee-server:8080/api/traefik/config`，接收覆盖所有运行中部署的动态生成 JSON 配置。
5. Traefik 立即开始将 `{project}.BASE_DOMAIN` 的请求路由到 `http://{node_ip}:{host_port}`。

### 生成的 Traefik 配置示例

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

`redirect-to-https` 中间件在 `traefik/dynamic.yml`（file provider）中静态定义。

## 需要认证的项目的 ForwardAuth 配置

当项目启用了 **Auth Required**（需要认证）时，HTTP provider 配置中会包含项目级 ForwardAuth 中间件：

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

该项目的 HTTPS 路由器会引用此中间件，强制要求 Google 登录后才能访问。

## 容器端口

每个项目都有一个**容器端口**配置（默认 `8080`）——即应用在容器内部监听的端口。部署节点会将此端口映射到随机的主机端口。你的 `Dockerfile` 应当 `EXPOSE`（或直接监听）所配置的端口。

## Traefik Provider

muvee 并行使用三个 Traefik Provider：

| Provider | 用途 |
|---|---|
| **HTTP** | 动态部署的项目容器（每 5 秒从 muvee-server 拉取） |
| **Docker** | 控制平面服务：muvee-server、Registry、Traefik 控制台（通过同一主机上的 Docker 标签发现） |
| **File** | 静态中间件：`redirect-to-https`、`muvee-forward-auth`、`muvee-forward-auth-admin` |

## Traefik 控制台

控制台地址为 `https://traefik.BASE_DOMAIN`，仅限管理员通过 ForwardAuth 认证后访问。只有 `ADMIN_EMAILS` 中列出的账号在使用 Google 登录后才能访问。

直接端口访问（`:8081`）已被移除——控制台只能通过 HTTPS 路由访问。
