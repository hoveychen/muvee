---
id: getting-started
title: 快速入门
sidebar_position: 1
---

# 快速入门

muvee 是一款轻量级自托管 PaaS，可将任意私有云转变为容器部署平台——内置数据仓库集成、LRU 缓存数据集注入，以及灵活的身份认证 Provider 支持。

## 前置条件

| 依赖项 | 说明 |
|---|---|
| Linux 主机 | 控制平面 + Agent 节点 |
| Docker + Docker Buildx | 所有节点均需安装 |
| NFS 共享 | 在所有部署节点挂载到相同路径 |
| PostgreSQL 16+ | 可在 Docker 中运行（`docker-compose.yml` 中已包含） |
| 身份认证 Provider | 至少配置一个：[Google](./auth/auth-google)、[飞书/Lark](./auth/auth-feishu)、[企业微信](./auth/auth-wecom)、[钉钉](./auth/auth-dingtalk) |

## 5 分钟快速启动

### 1. 克隆并配置

```bash
git clone https://github.com/hoveychen/muvee.git
cd muvee
cp .env.example .env
```

编辑 `.env`，配置至少一个身份认证 Provider。以 Google 为例：

```env
GOOGLE_CLIENT_ID=your-id.apps.googleusercontent.com
GOOGLE_CLIENT_SECRET=your-secret
ALLOWED_DOMAINS=your-company.com
ADMIN_EMAILS=admin@your-company.com
BASE_DOMAIN=example.com
JWT_SECRET=your-random-secret
ACME_EMAIL=admin@example.com
REGISTRY_USER=registry-user
REGISTRY_PASSWORD=a-strong-password
AGENT_SECRET=your-agent-secret   # 服务器与所有 Agent 之间的共享密钥
```

各 Provider 的配置指引见 [Authentication](./auth/auth-google) 章节。多个 Provider 可同时启用——登录页会为每个已配置的 Provider 显示一个登录按钮。

`ADMIN_EMAILS` 是以逗号分隔的邮箱列表，这些账号在登录时会自动获得 `admin` 角色，并可访问 `https://traefik.BASE_DOMAIN` 的 Traefik 控制台。

`AGENT_SECRET` 用于保护 `/api/agent/*` 端点。使用 `openssl rand -hex 32` 生成，并在服务器和所有 Agent 节点上使用相同的值。

### 2. 生成镜像仓库凭据

私有 Docker 镜像仓库使用 htpasswd 基础认证。在启动 Docker Compose **之前**，在控制平面主机上执行一次：

```bash
docker run --entrypoint htpasswd httpd:2 -Bbn \
  "$REGISTRY_USER" "$REGISTRY_PASSWORD" > registry/htpasswd
```

### 3. 启动控制平面

```bash
# 先创建 Docker 网络
docker network create muvee-net

# 启动所有服务
docker compose up -d
```

此命令将启动：
- **muvee-server** — API + 内嵌 Web UI，访问地址 `https://BASE_DOMAIN`
- **muvee-authservice** — Traefik ForwardAuth 边车服务（`:4181`）
- **PostgreSQL** — 元数据存储
- **Traefik** — 支持自动 HTTPS 的反向代理；控制台地址 `https://traefik.BASE_DOMAIN`（仅管理员可访问）
- **Registry** — 私有 Docker 镜像仓库，访问地址 `https://registry.BASE_DOMAIN`（htpasswd 认证）

### 4. 连接 Agent 节点

镜像仓库凭据（`REGISTRY_ADDR`、`REGISTRY_USER`、`REGISTRY_PASSWORD`）和 `BASE_DOMAIN` 会从控制平面**自动下发**。Agent 在启动时调用 `GET /api/agent/config`，并使用返回的值执行 `docker login`——无需在每个节点上单独配置凭据。

> **重要提示：** `CONTROL_PLANE_URL` 必须是控制平面的**内网地址**（而非公开域名）。Agent 使用此地址自动检测 Traefik 应使用哪个网络接口（及 IP）来访问已部署的容器。

```bash
# 构建节点
docker run -d --name muvee-agent \
  -e NODE_ROLE=builder \
  -e CONTROL_PLANE_URL=http://10.0.0.1:8080 \
  -e AGENT_SECRET=your-agent-secret \
  -v /var/run/docker.sock:/var/run/docker.sock \
  ghcr.io/hoveychen/muvee:latest agent

# 部署节点
docker run -d --name muvee-agent \
  -e NODE_ROLE=deploy \
  -e CONTROL_PLANE_URL=http://10.0.0.1:8080 \
  -e AGENT_SECRET=your-agent-secret \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /muvee/data:/muvee/data \
  -v /nfs/warehouse:/nfs/warehouse \
  ghcr.io/hoveychen/muvee:latest agent
```

### 5. 创建第一个项目

1. 打开 `https://example.com`，使用已配置的身份认证 Provider 登录
2. 点击 **New Project**（新建项目）
3. 填写 Git URL、分支、域名前缀
4. 点击 **Deploy**（部署）——muvee 将会：
   - 在构建节点上克隆你的代码仓库
   - 构建 Dockerfile
   - 将镜像推送到内部镜像仓库
   - 在部署节点上运行容器
   - 配置 Traefik 路由至 `{domain_prefix}.example.com`

## 下载二进制文件

适用于 Linux 和 macOS（amd64 + arm64）的预构建二进制文件可在 [Releases 页面](https://github.com/hoveychen/muvee/releases) 下载。

```bash
# 示例：下载并在 Linux amd64 上运行
curl -L https://github.com/hoveychen/muvee/releases/latest/download/muvee_linux_amd64.tar.gz | tar xz
./muvee_linux_amd64 server
```

`muvee server` 子命令内嵌了完整的 React Web UI——无需单独的 Web 服务器。
