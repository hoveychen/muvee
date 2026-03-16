---
id: installation
title: 安装部署
sidebar_position: 2
---

# 安装部署

## 方式 A：Docker Compose（推荐）

最快速的启动方式，所有服务均已预配置。

```bash
git clone https://github.com/hoveychen/muvee.git
cd muvee
cp .env.example .env
# 编辑 .env 填入你的配置值（BASE_DOMAIN、GOOGLE_*、JWT_SECRET、ADMIN_EMAILS、
#                              REGISTRY_USER、REGISTRY_PASSWORD、ACME_EMAIL）

# 启动前先生成 registry/htpasswd（私有镜像仓库必需）
docker run --entrypoint htpasswd httpd:2 -Bbn \
  "$REGISTRY_USER" "$REGISTRY_PASSWORD" > registry/htpasswd

# 单机 Standalone — 在本机同时启动控制面板和 Agent（默认，推荐）
docker compose up -d

# 多节点 — 仅启动控制面板，Agent 单独在工作节点上注册
# docker compose -f docker-compose.server.yml up -d
```

## 方式 B：预构建二进制文件

从 [Releases 页面](https://github.com/hoveychen/muvee/releases) 下载：

```bash
# Linux amd64
curl -L https://github.com/hoveychen/muvee/releases/latest/download/muvee_linux_amd64.tar.gz | tar xz
chmod +x muvee_linux_amd64

# 启动服务端（在 :8080 提供 Web UI）
export DATABASE_URL="postgres://..."
export GOOGLE_CLIENT_ID="..."
./muvee_linux_amd64 server
```

`muvee server` 子命令内嵌了 React Web UI——无需单独的静态文件服务器。

## 方式 C：从源码构建

依赖：Go 1.26+、Node 22+

```bash
git clone https://github.com/hoveychen/muvee.git
cd muvee
make build   # 构建前端 + 嵌入资源 + 编译单一二进制文件
ls bin/
# muvee
```

## Agent 节点

在每台工作节点上运行 `muvee agent`。单一二进制文件同时支持构建节点和部署节点两种角色。

构建节点**和**部署节点都需要镜像仓库凭据：构建节点在构建后推送镜像，部署节点在启动容器前拉取镜像。

:::caution CONTROL_PLANE_URL 必须使用内网地址
将 `CONTROL_PLANE_URL` 设置为控制平面的**内网地址**，而非公开域名。这样才能确保 Agent 的 `HOST_IP` 被正确自动检测（即 Traefik 用来访问容器的 IP）。
:::

```bash
# 构建节点
NODE_ROLE=builder \
CONTROL_PLANE_URL=http://10.0.0.1:8080 \
AGENT_SECRET=your-agent-secret \
REGISTRY_ADDR=registry.example.com \
REGISTRY_USER=registry-user \
REGISTRY_PASSWORD=a-strong-password \
./muvee agent

# 部署节点
NODE_ROLE=deploy \
CONTROL_PLANE_URL=http://10.0.0.1:8080 \
AGENT_SECRET=your-agent-secret \
REGISTRY_ADDR=registry.example.com \
REGISTRY_USER=registry-user \
REGISTRY_PASSWORD=a-strong-password \
DATA_DIR=/muvee/data \
BASE_DOMAIN=example.com \
./muvee agent
```

Agent 启动后会向控制平面注册自身，并每 5 秒轮询一次任务。Agent 要求主机上已安装并可访问 Docker。启动时，Agent 会自动使用提供的凭据执行 `docker login <REGISTRY_ADDR>`。
