---
id: project-mesh
title: 项目互访（muvee-mesh）
sidebar_position: 8
---

# 项目互访（muvee-mesh）

属于**同一个 owner** 的项目，可以在各自容器里直接用 HTTP 互相调用，不需要任何凭据：

```bash
# 在项目 "frontend" 的容器里
curl http://backend.muvee.internal/api/items
```

`backend` 是目标项目的**域名前缀**（`<prefix>.<BASE_DOMAIN>` 里的 `<prefix>`）。这次调用完全跳过目标项目的 ForwardAuth 登录；作为替代，muvee 会告诉目标是*哪个项目*在调用。

调用方项目不需要改代码、不需要任何配置——这个域名直接就能解析。

## 目标项目收到什么

经 mesh 转发的每个请求都带上：

| Header | 值 |
|---|---|
| `X-Muvee-Caller-Project` | 调用方项目名 |
| `X-Muvee-Caller-Project-Id` | 调用方项目 UUID |

`Host` 保持为 `<prefix>.muvee.internal`。调用方自己带的 `X-Muvee-Caller-*`、`X-Forwarded-User*` header 会在转发前被**删除**，所以调用方无法冒充别的项目或已登录用户。mesh 请求永远不带 `X-Forwarded-User`——项目间调用请用 caller header 做授权。

```python
# Python / Flask
caller = request.headers.get("X-Muvee-Caller-Project")
if caller is not None:
    ...  # 同 owner 的其他项目在调用
```

WebSocket 和流式响应（SSE / chunked）都能透传。

## 开关

开关在**被调用方**：「是否允许我名下的其他项目调用本项目」。**默认开启**，功能上线前已存在的项目也一样。

- Web 界面：项目 → **配置** → **项目互访** → *允许被其他项目访问*。
- 命令行：

  ```bash
  muveectl projects update PROJECT_ID --allow-project-access=false   # 拒绝
  muveectl projects update PROJECT_ID --allow-project-access=true    # 重新允许
  ```

改动约 5 秒内生效（网关会短暂缓存判定结果）。

## 网关的拒绝响应

请求没被转发时，由网关直接回复，body 为 `muvee mesh: <reason>`：

| 状态码 | reason | 含义 |
|---|---|---|
| 403 | `different_owner` | 目标属于另一个 owner |
| 403 | `access_disabled` | 目标关闭了「允许被其他项目访问」 |
| 403 | — | 发起请求的容器不是 mesh 上的 muvee 项目容器 |
| 404 | `target_not_found` | 没有这个域名前缀的项目 |
| 404 | `caller_not_found` | 调用方容器所属的项目已不存在 |
| 503 | `target_not_running` | 目标没有运行中的部署，或已暂停 |
| 502 | — | 控制面不可达，或目标端口拒绝连接 |

项目通过 mesh 调用自己也是允许的。

## 范围与限制

- **只支持 HTTP、端口 80。** 只能 `http://<prefix>.muvee.internal/`，不支持 HTTPS，也不支持其他端口。TCP 路由项目（`--tcp-domain`）无法通过 mesh 访问。
- **哪些容器能发起调用。** 单容器项目（Dockerfile 项目、镜像项目）自动加入 mesh。**compose** 项目只有 **expose service** 加入；其余 service 仍只在自己的 compose 私有网络里，解析不了 `*.muvee.internal`。设置了 `network_mode` 的 service 不会加入。
- **已有容器要重新部署才能作为调用方**：mesh 网络和 DNS 是在创建容器时挂上的。被调用方不需要重新部署——网关和 Traefik 一样，通过 `HOST_IP:端口` 访问它。
- **deploy agent 必须以 docker 容器方式运行。** 网关跑在 deploy agent 里，只有 agent 检测到自己运行在容器中（标准的 `docker-compose.yml` / `docker-compose.agent-deploy.yml` 部署）时才启用。直接在宿主机上运行的 agent 会打印 `muvee-mesh gateway disabled`，部署时不挂 mesh。

## 工作原理

```
 项目 A 容器 ──DNS: backend.muvee.internal?──▶ 网关 10.213.0.2（deploy agent）
      │                                            │ 回答 10.213.0.2
      └──HTTP Host: backend.muvee.internal ───────▶│
                                                   │ 按源 IP 认出 caller = A
                                                   │ GET /api/agent/mesh/resolve?caller=A&target=backend
                                                   │   同 owner？允许访问？在运行？
                                                   └──▶ backend 的 HOST_IP:端口（注入 caller header）
```

1. 每个 deploy 节点上有一个 docker 网络 `muvee-mesh`（默认子网 `10.213.0.0/16`）。deploy agent 以固定 IP（默认 `10.213.0.2`）接入，并在这个 IP 上跑 DNS 和 HTTP 反代。
2. 项目容器加入 `muvee-mesh`，并把网关设为第一个 DNS。`*.muvee.internal` 解析到网关；其余查询转发给 agent 自己的上游 DNS。
3. 网关按请求在 `muvee-mesh` 上的源 IP 认出调用方（对应容器的 `muvee.domain_prefix` label），向控制面询问是否允许，再转发到目标的运行中部署。

### Agent 配置

全部可选；每台 docker 主机只跑一套 muvee 栈时，默认值即可工作。

| 变量 | 默认值 | 说明 |
|---|---|---|
| `MESH_NETWORK` | `muvee-mesh` | mesh docker 网络名。同一台 docker 主机上跑多套 muvee 栈时必须设置（见下）。 |
| `MESH_SUBNET` | `10.213.0.0/16` | agent 创建 `muvee-mesh` 时用的子网。网络已存在则沿用其实际子网。只有和内网/VPN 网段冲突时才需要改。 |
| `MESH_GATEWAY_IP` | `10.213.0.2` | agent 在 `muvee-mesh` 上的固定 IP，必须在子网内。 |
| `MESH_FALLBACK_DNS` | 自动检测 | 逗号分隔，作为第二个 DNS 交给项目容器，保证网关不可用时外网解析仍可用。默认取 docker 给默认 bridge 上容器的上游 DNS。 |
| `MESH_SELF_CONTAINER` | 自动检测 | agent 自身的容器名/ID，仅当从 `/proc/self/mountinfo` 自动识别失败时需要设置。 |

**同一台 docker 主机上有多套 muvee 栈**（例如两个控制面的 deploy agent 跑在同一台机器上）：docker 网络是整机共享的，每套栈的 deploy agent **必须**各自设置不同的 `MESH_NETWORK` 和互不重叠的 `MESH_SUBNET`（例如 `MESH_NETWORK=insnap-mesh`、`MESH_SUBNET=10.214.0.0/16`）。用默认值时两个 agent 会在同一个 `muvee-mesh` 上争抢 `10.213.0.2`。

- **复制某套栈的 `.env` / compose 目录来建新栈时**：`MESH_NETWORK` 和 `MESH_SUBNET` 都要改。只改子网不改名没有用：同名网络已存在时会被直接复用，用的是它自己的子网。
- **`MESH_NETWORK` 必须是专用网络**，由 agent 创建并打上 `muvee.mesh=1` 标签。已存在但没有这个标签的网络（例如栈自己的 compose 网络），agent 一律不碰：打印 `mesh: docker network ... is not a muvee mesh network`，mesh 保持关闭，也绝不改动自己在该网络上的连接。
- **网关 IP 已被占用**：agent 接入前会先检查网关 IP。被其他容器占用时（运行中且就在这个 IP 上，或已停止 / 已创建未启动但仍把它预留为固定 IP），打印 `mesh: gateway IP ... is already held by container ...`，mesh 保持关闭并每 30 秒重试；agent 本身照常工作，其容器也能正常重启。*运行中*的容器若只是残留了该 IP 的配置条目（没有实际地址），不算占用。
- **被另一套栈占用**：占用者属于另一个 compose 项目（不是 compose 起的则按容器名区分）时，agent 打印 `mesh: WARNING: gateway IP ... is held by ... of another muvee stack (compose project ...)`，并把它**记下来**，存在 `$DATA_DIR/mesh-foreign-gateway-holders.json`（agent 的数据卷，升级重建后仍在）。此后在这个网络上 agent 再也不抢网关 IP，即使它看起来空出来了——否则 Watchtower 重建另一套栈的 agent 时（先删旧容器、再建新容器）会被它趁空抢走，害得那套健康的栈起不来。记录按网络区分，网络重建后自动失效。确实要重试时，删掉这个文件并重启 agent，或改 `MESH_NETWORK`。
- **已经占着网关的一方也可能被抢**：只要两套栈还共用网络，另一个 agent 仍可能在升级空窗里抢走 IP（例如它跑的是旧版 muvee，或它的记录被删了），这时升级后的新 agent 容器停在 `Created`，启动报 `Address already in use`。恢复：`docker rm <卡住的容器>`，再重建 agent（`docker compose up -d <agent 服务名>`），然后把两套栈的网络分开。
- **改了 `MESH_NETWORK` 后，必须重新部署该栈的全部项目。** agent 会建新网络，但之前部署的项目还留在旧网络上，DNS 仍指向旧网络上的网关 IP——那可能已经是*另一套*栈的 agent。网关只按调用方容器的 `muvee.domain_prefix` 标签认身份，留在旧网络上的项目会被当成另一套栈里同前缀的项目。


### 网关不可用时

agent 重启或升级期间，网关会短暂不可用：

- `*.muvee.internal` 调用会失败，agent 恢复后自动重新接入网络，调用自动恢复。
- **项目容器的外网 DNS 会变慢，但不会挂：** 第一个 DNS（网关）超时后由兜底 DNS 应答。在 Docker 29 上实测，agent 停止期间每次解析约 **8 秒**，agent 恢复后立即回到正常。

## 安全说明

mesh 是在你信任的节点内部提供的便利边界，不是硬隔离边界。

- **跨 owner 直连是可能的（v1 已知限制）。** 所有调用方共用 `muvee-mesh` 网络，容器可以绕过网关，**按 mesh IP 和容器端口**直接连到其他项目的容器，不论 owner。网关的 owner 校验和「允许访问」开关只对经 `<prefix>.muvee.internal` 发起的调用生效。
- **已发布端口对任何项目容器都可达。** 这在 mesh 之前就存在：节点上的任何项目容器（不论是否在 mesh 上）都能直连任一项目的 `HOST_IP:<宿主端口>`，绕过 Traefik，也就绕过了 ForwardAuth——而且这种直连请求可以随意携带 `X-Forwarded-User` 或 `X-Muvee-Caller-*`。ForwardAuth 和这些 header 防的是公网，防不了同节点上的其他工作负载。

如果某个项目绝不能被节点上的其他工作负载访问，请把它部署在只与可信项目共享的节点上。
