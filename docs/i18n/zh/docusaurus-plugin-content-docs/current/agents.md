---
id: agents
title: Agent 节点
sidebar_position: 8
---

# Agent 节点

单个 `muvee-agent` 二进制文件同时支持构建节点和部署节点两种角色，具体角色由 `NODE_ROLE` 环境变量决定。

## 安全性：Agent 密钥

所有 Agent ↔ 控制平面的通信均通过**共享密钥**（`AGENT_SECRET`）保护。Agent 在每个请求中通过 `X-Agent-Secret` HTTP 头传递此值。服务器会拒绝头部缺失或不正确的请求，返回 `401 Unauthorized`。

在控制平面和所有 Agent 节点上设置相同的 `AGENT_SECRET` 值。使用以下命令生成强密钥：

```bash
openssl rand -hex 32
```

若未设置 `AGENT_SECRET`，服务器会记录警告并接受所有 Agent 请求（仅适用于本地开发环境）。

## 通信协议

Agent 采用**长轮询拉取模型**——控制平面无需向 Agent 发起入站连接。

```
Agent → POST /api/agent/register                  （启动时）
     header: X-Agent-Secret: <secret>
     body: { hostname, role, host_ip, max_storage }
     ← Node（含分配的节点 ID）

Agent → GET  /api/agent/config                    （启动时，register 之后）
     header: X-Agent-Secret: <secret>
     ← { registry_addr, registry_user, registry_password, base_domain }

Agent → GET  /api/agent/tasks?node_id={id}        （每 5 秒）
     header: X-Agent-Secret: <secret>
     ← []Task（此节点的待处理任务）

Agent → POST /api/agent/tasks/{id}/complete
     header: X-Agent-Secret: <secret>
     body: { status, result, image_tag? }          # 构建任务
     body: { status, host_port }                   # 部署任务
```

Agent 只需要能够访问 `CONTROL_PLANE_URL` 的出站连接——可以位于 NAT 或防火墙后面。

:::caution CONTROL_PLANE_URL 必须使用内网地址
将 `CONTROL_PLANE_URL` 设置为控制平面的**内网地址**（如 `http://10.0.0.1:8080`），而非公开域名。

原因如下：
1. Agent 通过观察连接控制平面所使用的网络接口来自动检测 `HOST_IP`。使用内网地址能确保选择正确的接口（及 IP），从而让 Traefik 能够正确路由流量到容器。
2. 没有必要走公网——Agent 端点不受 OAuth 保护。
:::

## 心跳机制

Agent 在启动时（以及重连时定期）发送注册请求。注册信息中包含节点的 `host_ip`——即 Traefik 用于访问此节点上已部署容器的 IP。该 IP 通过查找连接控制平面所使用的本地网络接口自动检测。

若节点的 `last_seen_at` 超过 2 分钟未更新，控制平面将其标记为离线。离线节点不参与调度。

## 镜像仓库认证

构建节点和部署节点都需要向私有镜像仓库进行认证：

- **构建节点** — 推送新构建的镜像（`docker buildx build --push`）
- **部署节点** — 启动容器前拉取镜像（`docker run` 若镜像未在本地缓存则触发隐式 `docker pull`）

镜像仓库凭据**自动下发**。Agent 在启动时调用 `GET /api/agent/config`，从控制平面获取 `registry_addr`、`registry_user` 和 `registry_password`，随后自动执行 `docker login <registry_addr>`。你只需在控制平面上设置一次 `REGISTRY_ADDR`、`REGISTRY_USER` 和 `REGISTRY_PASSWORD`。

### REGISTRY_ADDR：公网地址 vs 内网地址

`REGISTRY_ADDR` 仅供 Agent 节点使用（用于 `docker login`、镜像推送和拉取）。控制平面不直接访问镜像仓库，因此无需将镜像仓库暴露在公网域名上。

若所有 Agent 节点与镜像仓库在同一内网，可以将 `REGISTRY_ADDR` 指向内网地址，而非经 Traefik 代理的公网域名：

| 部署方式 | REGISTRY_ADDR 示例 |
|---|---|
| 通过 Traefik 的公网域名（默认） | `registry.example.com` |
| 与 Registry 容器同一 Docker 网络 | `registry:5000` |
| 同一局域网 / VPC | `10.0.0.1:5000` |

:::caution 使用 HTTP 明文镜像仓库需要配置 Docker Daemon
内置的 Registry 容器（`registry:2`）在 5000 端口上使用明文 **HTTP** 通信。Traefik 在边缘添加 TLS，因此公网域名可直接使用。当使用绕过 Traefik 的内网地址时，连接为未加密的 HTTP，Docker 默认会拒绝推送或拉取。

在**每个 Agent 节点**上将内网地址添加到 `insecure-registries`：

```json title="/etc/docker/daemon.json"
{
  "insecure-registries": ["10.0.0.1:5000"]
}
```

然后重启 Docker：

```bash
sudo systemctl restart docker
```
:::

对于同机房的节点，通常推荐使用内网地址——可以避免公网往返开销，并消除对 DNS / Let's Encrypt 的依赖。

## 构建节点

依赖：
- `git` 命令行工具
- 支持 `buildx` 的 `docker` 命令行工具

收到构建任务后：

1. `git clone --depth=1 --branch {branch} {git_url}` 到临时目录
2. `docker buildx build -f {dockerfile} -t {registry}/{project}:{sha} --push`
3. 携带 `image_tag` 报告完成

## 部署节点

依赖：
- `docker` 命令行工具
- `rsync`（用于 dependency 数据集同步）
- NFS 挂载在与每个数据集 `nfs_path` 配置相同的路径上
- 能够访问控制平面的网络连接（`CONTROL_PLANE_URL`）

收到部署任务后：

1. 对每个 `dependency` 数据集：从 NFS rsync，并在部署挂载目录中创建符号链接
2. 对每个 `readwrite` 数据集：准备直接 NFS bind-mount 路径
3. `docker rm -f muvee-{domain_prefix}`（滚动更新：停止旧容器）
4. `docker run -d --name muvee-{domain_prefix} -p 0:{container_port} ... {image_tag}` — Docker 分配随机主机端口
5. `docker port muvee-{domain_prefix} {container_port}` — 发现已分配的主机端口
6. 携带 `host_port` 报告完成；控制平面更新 Traefik HTTP provider 配置
7. Traefik 在 5 秒内接收新路由
