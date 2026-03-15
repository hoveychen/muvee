<div align="center">

<h1>muvee</h1>

<p>
  <a href="https://github.com/hoveychen/muvee/actions/workflows/ci.yml">
    <img src="https://github.com/hoveychen/muvee/actions/workflows/ci.yml/badge.svg" alt="CI" />
  </a>
  <a href="https://github.com/hoveychen/muvee/releases/latest">
    <img src="https://img.shields.io/github/v/release/hoveychen/muvee?color=c8f03c" alt="Latest Release" />
  </a>
  <a href="https://github.com/hoveychen/muvee/blob/main/LICENSE">
    <img src="https://img.shields.io/badge/license-Apache%202.0-blue.svg" alt="License" />
  </a>
  <a href="https://goreportcard.com/report/github.com/hoveychen/muvee">
    <img src="https://goreportcard.com/badge/github.com/hoveychen/muvee" alt="Go Report Card" />
  </a>
  <img src="https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go" alt="Go Version" />
  <img src="https://img.shields.io/badge/platform-linux%20%7C%20macos-lightgrey" alt="Platform" />
</p>

<p><strong>Lightweight self-hosted PaaS for private clouds — Git → Docker → Deploy, with smart data warehouse integration.</strong></p>

<p>
  <a href="#english">English</a> · <a href="#中文">中文</a> ·
  <a href="https://hoveychen.github.io/muvee">Docs</a> ·
  <a href="https://github.com/hoveychen/muvee/releases">Releases</a>
</p>

</div>

---

## English

### What is muvee?

muvee is a **self-hosted PaaS** designed for private clouds with many lightweight applications. No Kubernetes, no YAML sprawl. Push a tag → binary is released. Connect a Git repo → container is deployed.

Key features:

- **Git → Docker → Deploy** — point at a repo with a `Dockerfile`, click Deploy. muvee builds the image on a builder node, pushes to its internal registry, and runs the container on a deploy node, wired to `{project}.yourdomain.com` automatically via Traefik.
- **Data Warehouse Integration** — declare NFS-backed datasets as dependencies. muvee rsyncs them to the deploy node (LRU-cached, versioned) and mounts them into the container at `/data/{name}`. Or declare a dataset as `readwrite` for a direct NFS bind-mount.
- **Smart Affinity Scheduling** — deploy nodes are scored by how many required datasets they already have cached, minimizing rsync time.
- **File-level Dataset Tracking** — a background monitor scans NFS paths, diffs file trees, and records a `git log`–style history of every file change (added / modified / deleted), with per-file timelines in the UI.
- **Per-project Google Auth** — optionally require Google sign-in to access deployed apps, with email domain allowlists.
- **Single binary** — one `muvee` binary runs as `muvee server`, `muvee agent`, or `muvee authservice`. The server subcommand embeds the React web UI — no separate web server needed.

### Quick Start

**Prerequisites — DNS**

Point a wildcard A record at your VPS IP before starting:

```
Type  Name               Value
A     www.example.com    <your-vps-ip>
A     *.example.com      <your-vps-ip>   (covers all project subdomains)
```

Make sure ports **80** and **443** are open on the VPS firewall. Traefik will obtain a Let's Encrypt certificate automatically.

**1 — Create Google OAuth2 credentials**

muvee uses Google OAuth2 for user authentication. Before configuring, create credentials in [Google Cloud Console](https://console.cloud.google.com/apis/credentials):

1. Click **Create Credentials → OAuth client ID**, select **Web application**
2. Under **Authorized redirect URIs**, add **both**:
   ```
   https://www.YOUR_BASE_DOMAIN/auth/google/callback
   https://www.YOUR_BASE_DOMAIN/_oauth
   ```
   The first is for the control-panel login; the second is for per-project ForwardAuth.
3. Copy the **Client ID** and **Client Secret**

**2 — Configure**

```bash
git clone https://github.com/hoveychen/muvee.git && cd muvee
cp .env.example .env
# Edit .env — fill in BASE_DOMAIN, GOOGLE_CLIENT_ID/SECRET, JWT_SECRET, ACME_EMAIL,
#             ADMIN_EMAILS, REGISTRY_USER, REGISTRY_PASSWORD,
#             AGENT_SECRET (shared secret for agent authentication)
```

**3 — Generate registry credentials**

The private registry requires a htpasswd file. Run once on the control plane host:

```bash
docker run --entrypoint htpasswd httpd:2 -Bbn <REGISTRY_USER> <REGISTRY_PASSWORD> > registry/htpasswd
```

Use the same `REGISTRY_USER` / `REGISTRY_PASSWORD` values you set in `.env`.

**4 — Start the control plane**

```bash
docker network create muvee-net
docker compose up -d
```

Traefik is now listening on 443. Open `https://www.BASE_DOMAIN` and sign in with Google. Admin accounts listed in `ADMIN_EMAILS` also gain access to `https://traefik.BASE_DOMAIN`.

**5 — Register worker nodes**

Run the following on each worker machine. Registry credentials and `BASE_DOMAIN` are **automatically distributed** from the control plane — agents fetch them via `/api/agent/config` on startup, so you don't need to configure them per node.

> `CONTROL_PLANE_URL` must be the **internal network address** of the control plane (e.g. `http://10.0.0.1:8080`), not the public domain. The agent uses this to detect the correct network interface for Traefik routing.

```bash
# Builder node
docker run -d --name muvee-agent --restart unless-stopped \
  -e NODE_ROLE=builder \
  -e CONTROL_PLANE_URL=http://10.0.0.1:8080 \
  -e AGENT_SECRET=<your-agent-secret> \
  -v /var/run/docker.sock:/var/run/docker.sock \
  ghcr.io/hoveychen/muvee:latest agent

# Deploy node
docker run -d --name muvee-agent --restart unless-stopped \
  -e NODE_ROLE=deploy \
  -e CONTROL_PLANE_URL=http://10.0.0.1:8080 \
  -e AGENT_SECRET=<your-agent-secret> \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /muvee/data:/muvee/data \
  -v /nfs/warehouse:/nfs/warehouse \
  ghcr.io/hoveychen/muvee:latest agent
```

> **No Docker?** Download the binary and run `muvee agent` directly — see [Release Binaries](#release-binaries).

See the **[full documentation →](https://hoveychen.github.io/muvee)**

### Architecture at a Glance

```
Control Plane (single host)
  muvee-server    — API + Web UI (Go, embeds React)
  muvee-authsvc   — Traefik ForwardAuth sidecar
  PostgreSQL      — metadata
  Traefik v3      — HTTPS reverse proxy, auto-routing
  Distribution    — private Docker registry

Worker Nodes (any number, any mix)
  muvee-agent     — builder or deploy role, polls control plane
```

### Release Binaries

**Server / Agent** — pre-built for Linux and macOS (amd64 + arm64):

```bash
curl -L https://github.com/hoveychen/muvee/releases/latest/download/muvee_linux_amd64.tar.gz | tar xz
./muvee_linux_amd64 server      # starts API + web UI on :8080, auto-migrates DB
./muvee_linux_amd64 agent       # worker node (set NODE_ROLE=builder or deploy)
./muvee_linux_amd64 authservice # Traefik ForwardAuth sidecar
```

**muveectl** — CLI client for Linux, macOS, and Windows:

```bash
# macOS (Apple Silicon)
curl -Lo muveectl https://github.com/hoveychen/muvee/releases/latest/download/muveectl_darwin_arm64
chmod +x muveectl && sudo mv muveectl /usr/local/bin/

# Linux (amd64)
curl -Lo muveectl https://github.com/hoveychen/muvee/releases/latest/download/muveectl_linux_amd64
chmod +x muveectl && sudo mv muveectl /usr/local/bin/
```

```bash
muveectl login --server https://www.example.com  # first-time setup
muveectl projects list
muveectl projects deploy PROJECT_ID
```

See the [muveectl CLI reference](https://hoveychen.github.io/muvee/docs/muveectl) for full command documentation.

### Prerequisites

| | |
|---|---|
| Control plane host | Linux, Docker |
| Worker nodes | Linux, Docker + Buildx, rsync, `git` |
| NFS share | Mounted at the same path on all deploy nodes |
| PostgreSQL 16+ | Can use the bundled Docker Compose service |
| Google OAuth2 | [Create credentials](https://console.cloud.google.com/apis/credentials) |

### Documentation

- [Getting Started](https://hoveychen.github.io/muvee/docs/getting-started)
- [Installation](https://hoveychen.github.io/muvee/docs/installation)
- [Configuration Reference](https://hoveychen.github.io/muvee/docs/configuration)
- [Architecture](https://hoveychen.github.io/muvee/docs/architecture)
- [Scheduler & Affinity](https://hoveychen.github.io/muvee/docs/scheduler)
- [Dataset Monitor](https://hoveychen.github.io/muvee/docs/dataset-monitor)
- [ForwardAuth & Access Control](https://hoveychen.github.io/muvee/docs/forward-auth)
- [muveectl CLI](https://hoveychen.github.io/muvee/docs/muveectl)

---

## 中文

### muvee 是什么？

muvee 是一个面向私有云的**轻量自托管 PaaS**，专为大量小型应用的快速上线设计。不需要 Kubernetes，不需要复杂配置。绑定 Git 仓库，点击发布，应用即上线。

核心功能：

- **Git → Docker → 部署** — 指向一个含 `Dockerfile` 的仓库，点击"发布"。muvee 在 Builder 节点构建镜像，推送到内部 Registry，在 Deploy 节点启动容器，并通过 Traefik 自动路由到 `{project}.yourdomain.com`。
- **数据仓库集成** — 在 Project 中声明 NFS 上的数据集作为依赖。muvee 将数据 rsync 到部署节点（LRU 缓存、版本管理），并以 `/data/{name}` 挂载进容器。也支持 `readwrite` 模式，直接 NFS bind-mount，不拷贝。
- **亲和性调度** — 部署节点根据已缓存的数据集数量打分，优先选择无需大量 rsync 的节点，最小化数据同步开销。
- **文件级数据追踪** — 后台 Monitor 定时扫描 NFS 路径，对比文件树，记录每个文件的 added / modified / deleted 历史，UI 提供类似 `git log --follow` 的单文件时间轴视图。
- **per-project Google 登录** — 可选要求访问部署应用前先通过 Google 登录，支持邮箱域名白名单。
- **单一二进制** — 一个 `muvee` 二进制，通过子命令区分角色：`muvee server` / `muvee agent` / `muvee authservice`。`server` 子命令内嵌 React 前端，无需额外 Web 服务器。

### 5 分钟快速部署

**前置条件 — DNS 配置**

在启动之前，先将以下 DNS 记录指向你的 VPS 公网 IP：

```
类型  主机名                值
A     www.example.com      <你的 VPS IP>
A     *.example.com        <你的 VPS IP>   （覆盖所有 project 子域名）
```

确保 VPS 防火墙放行 **80** 和 **443** 端口。Traefik 会自动向 Let's Encrypt 申请 HTTPS 证书。

**第 1 步 — 创建 Google OAuth2 凭证**

muvee 使用 Google OAuth2 进行用户认证。启动前先在 [Google Cloud Console](https://console.cloud.google.com/apis/credentials) 创建凭证：

1. 点击**创建凭证 → OAuth 客户端 ID**，应用类型选**网页应用**
2. 在**已授权的重定向 URI** 中添加**以下两个**：
   ```
   https://www.你的域名/auth/google/callback
   https://www.你的域名/_oauth
   ```
   第一个用于控制台登录，第二个用于各 project 的 ForwardAuth 认证。
3. 复制生成的 **Client ID** 和 **Client Secret**

**第 2 步 — 配置**

```bash
git clone https://github.com/hoveychen/muvee.git && cd muvee
cp .env.example .env
# 编辑 .env，填写 BASE_DOMAIN、GOOGLE_CLIENT_ID/SECRET、JWT_SECRET、ACME_EMAIL
#             ADMIN_EMAILS（管理员邮箱）、REGISTRY_USER、REGISTRY_PASSWORD
#             AGENT_SECRET（agent 认证共享密钥，所有 agent 节点需相同）
```

**第 3 步 — 生成 Registry 凭证**

私有镜像仓库需要 htpasswd 文件做基础认证，在控制平面主机上执行一次：

```bash
docker run --entrypoint htpasswd httpd:2 -Bbn <REGISTRY_USER> <REGISTRY_PASSWORD> > registry/htpasswd
```

`REGISTRY_USER` / `REGISTRY_PASSWORD` 与 `.env` 中的配置保持一致。

**第 4 步 — 启动控制平面**

```bash
docker network create muvee-net
docker compose up -d
```

Traefik 开始监听 443 端口。在浏览器中打开 `https://www.BASE_DOMAIN`，使用 Google 账号登录。`ADMIN_EMAILS` 中配置的管理员账号还可访问 `https://traefik.BASE_DOMAIN` Traefik 控制面板。

**第 5 步 — 注册工作节点**

在每台工作机器上执行。Registry 凭证和 `BASE_DOMAIN` 由控制平面**自动下发** —— Agent 启动后会通过 `/api/agent/config` 接口拉取，无需在每个节点手动配置。

> `CONTROL_PLANE_URL` 必须填写控制平面的**内网地址**（如 `http://10.0.0.1:8080`），不要使用公网域名。Agent 通过该地址自动探测正确的出网接口，确保 Traefik 能路由到已部署的容器。

```bash
# Builder 节点
docker run -d --name muvee-agent --restart unless-stopped \
  -e NODE_ROLE=builder \
  -e CONTROL_PLANE_URL=http://10.0.0.1:8080 \
  -e AGENT_SECRET=<your-agent-secret> \
  -v /var/run/docker.sock:/var/run/docker.sock \
  ghcr.io/hoveychen/muvee:latest agent

# Deploy 节点
docker run -d --name muvee-agent --restart unless-stopped \
  -e NODE_ROLE=deploy \
  -e CONTROL_PLANE_URL=http://10.0.0.1:8080 \
  -e AGENT_SECRET=<your-agent-secret> \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /muvee/data:/muvee/data \
  -v /nfs/warehouse:/nfs/warehouse \
  ghcr.io/hoveychen/muvee:latest agent
```

> **不用 Docker？** 直接下载二进制运行 `muvee agent`，详见[下载预编译二进制](#下载预编译二进制)。

### 整体架构

```
控制平面（单台主机）
  muvee-server    — API + Web UI（Go，内嵌 React）
  muvee-authsvc   — Traefik ForwardAuth 认证服务
  PostgreSQL      — 元数据存储
  Traefik v3      — HTTPS 反向代理，自动路由
  Distribution    — 私有 Docker 镜像仓库

工作节点（任意数量，混合角色）
  muvee-agent     — builder 或 deploy 角色，轮询控制平面获取任务
```

### 下载预编译二进制

**服务端 / Agent** — 支持 Linux / macOS（amd64 + arm64）：

```bash
curl -L https://github.com/hoveychen/muvee/releases/latest/download/muvee_linux_amd64.tar.gz | tar xz
./muvee_linux_amd64 server      # 启动 API + Web UI，监听 :8080，自动执行数据库迁移
./muvee_linux_amd64 agent       # 工作节点（设置 NODE_ROLE=builder 或 deploy）
./muvee_linux_amd64 authservice # Traefik ForwardAuth 认证服务
```

**muveectl** — 命令行客户端，支持 Linux / macOS / Windows：

```bash
# macOS (Apple Silicon)
curl -Lo muveectl https://github.com/hoveychen/muvee/releases/latest/download/muveectl_darwin_arm64
chmod +x muveectl && sudo mv muveectl /usr/local/bin/

# Linux (amd64)
curl -Lo muveectl https://github.com/hoveychen/muvee/releases/latest/download/muveectl_linux_amd64
chmod +x muveectl && sudo mv muveectl /usr/local/bin/
```

```bash
muveectl login --server https://www.example.com  # 首次配置
muveectl projects list
muveectl projects deploy PROJECT_ID
```

完整命令参考见 [muveectl CLI 文档](https://hoveychen.github.io/muvee/docs/muveectl)。

### 环境要求

| | |
|---|---|
| 控制平面主机 | Linux，Docker |
| 工作节点 | Linux，Docker + Buildx，rsync，`git` |
| NFS 共享 | 所有 Deploy 节点以相同路径挂载 |
| PostgreSQL 16+ | 可使用内置 Docker Compose 服务 |
| Google OAuth2 | [创建凭证](https://console.cloud.google.com/apis/credentials) |

### 详细文档

- [快速开始](https://hoveychen.github.io/muvee/docs/getting-started)
- [安装](https://hoveychen.github.io/muvee/docs/installation)
- [配置参考](https://hoveychen.github.io/muvee/docs/configuration)
- [整体架构](https://hoveychen.github.io/muvee/docs/architecture)
- [调度器与亲和性](https://hoveychen.github.io/muvee/docs/scheduler)
- [数据集监控](https://hoveychen.github.io/muvee/docs/dataset-monitor)
- [ForwardAuth 与访问控制](https://hoveychen.github.io/muvee/docs/forward-auth)
- [muveectl CLI](https://hoveychen.github.io/muvee/docs/muveectl)

---

## Contributing

Issues and pull requests are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) if you'd like to contribute.

## License

Apache 2.0 — see [LICENSE](LICENSE)
