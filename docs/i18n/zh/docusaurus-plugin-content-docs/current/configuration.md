---
id: configuration
title: 配置参考
sidebar_position: 3
---

# 配置参考

所有配置均通过环境变量设置。

## 控制平面（`muvee-server`）

| 变量 | 默认值 | 说明 |
|---|---|---|
| `DATABASE_URL` | `postgres://muvee:muvee@localhost:5432/muvee?sslmode=disable` | PostgreSQL 连接字符串 |
| `MIGRATIONS_DIR` | `./db/migrations` | SQL 迁移文件路径 |
| `PORT` | `8080` | HTTP 监听端口 |
| `BASE_DOMAIN` | `localhost` | 根域名；项目以 `{prefix}.BASE_DOMAIN` 的形式对外提供服务。同时通过 `/api/agent/config` 下发给 Agent。 |
| `GOOGLE_CLIENT_ID` | — | Google OAuth2 客户端 ID。设置后启用 Google 登录。详见 [Google OAuth2](./auth/google)。 |
| `GOOGLE_CLIENT_SECRET` | — | Google OAuth2 客户端密钥 |
| `GOOGLE_REDIRECT_URL` | `http://localhost:8080/auth/google/callback` | Google OAuth2 回调地址 |
| `FEISHU_APP_ID` | — | 飞书 / Lark App ID。设置后启用飞书登录。详见 [飞书 / Lark](./auth/feishu)。 |
| `FEISHU_APP_SECRET` | — | 飞书 / Lark App Secret |
| `FEISHU_REDIRECT_URL` | `http://localhost:8080/auth/feishu/callback` | 飞书 OAuth2 回调地址 |
| `FEISHU_BASE_URL` | `https://open.feishu.cn` | 飞书 API 基础地址。国际版 Lark 设置为 `https://open.larksuite.com`。 |
| `WECOM_CORP_ID` | — | 企业微信 CorpId（企业ID）。设置后启用企业微信登录。详见 [企业微信](./auth/wecom)。 |
| `WECOM_CORP_SECRET` | — | 企业微信自建应用的 App Secret |
| `WECOM_AGENT_ID` | — | 企业微信自建应用的 AgentId |
| `WECOM_REDIRECT_URL` | `http://localhost:8080/auth/wecom/callback` | 企业微信 OAuth2 回调地址 |
| `DINGTALK_CLIENT_ID` | — | 钉钉 AppKey。设置后启用钉钉登录。详见 [钉钉](./auth/dingtalk)。 |
| `DINGTALK_CLIENT_SECRET` | — | 钉钉 AppSecret |
| `DINGTALK_REDIRECT_URL` | `http://localhost:8080/auth/dingtalk/callback` | 钉钉 OAuth2 回调地址 |
| `ALLOWED_DOMAINS` | _（允许所有）_ | 允许登录的邮箱域名，逗号分隔（如 `company.com`）。仅对 Google 生效；飞书/企微/钉钉在无法获取真实邮箱时会生成合成地址（`*.local`），这类地址会自动跳过域名校验。 |
| `ADMIN_EMAILS` | — | 逗号分隔的邮箱地址列表，登录时自动提升为 `admin` 角色，可访问 `traefik.BASE_DOMAIN` |
| `JWT_SECRET` | `change-me-in-production` | 用于签发 JWT 会话令牌的密钥 |
| `AGENT_SECRET` | — | Agent ↔ 服务器认证的共享密钥（所有 Agent 上设置相同的值）。未设置时，Agent 端点无需认证（仅用于开发环境）。 |
| `AUTH_SERVICE_URL` | `http://muvee-authservice:4181` | `muvee-authservice` 的内部 URL；在为 Traefik 生成项目级 ForwardAuth 配置时使用 |
| `REGISTRY_ADDR` | `localhost:5000` | Docker 镜像仓库地址。通过 `/api/agent/config` 下发给 Agent——Agent 无需在本地设置此项。 |
| `REGISTRY_USER` | — | 镜像仓库基础认证用户名。下发给 Agent——Agent 启动时自动执行 `docker login`。 |
| `REGISTRY_PASSWORD` | — | 镜像仓库基础认证密码。下发给 Agent。 |
| `SECRET_ENCRYPTION_KEY` | — | 64 字符十六进制字符串（32 字节），用于以 AES-256-GCM 加密静态密钥。启用 Secrets 功能时必填。使用 `openssl rand -hex 32` 生成。 |
| `VOLUME_NFS_BASE_PATH` | — | 控制平面主机上用于项目工作区卷的 NFS 基础目录（如 `/mnt/nfs/volumes`）。每个项目的子目录会在该路径下自动创建。同时通过 `/api/agent/config` 下发给部署 Agent，Agent 使用该路径将卷 bind mount 到容器中。未设置时工作区功能不可用。 |

## ForwardAuth 服务（`muvee-authservice`）

| 变量 | 默认值 | 说明 |
|---|---|---|
| `PORT` | `4181` | HTTP 监听端口 |
| `GOOGLE_CLIENT_ID` | — | 与控制平面相同 |
| `GOOGLE_CLIENT_SECRET` | — | 与控制平面相同 |
| `FORWARD_AUTH_REDIRECT_URL` | `http://localhost:4181/_oauth` | ForwardAuth 的 OAuth2 回调 URL。生产环境设置为 `https://BASE_DOMAIN/_oauth`，并在 Google Cloud Console 中与 `GOOGLE_REDIRECT_URL` 一起注册。 |
| `BASE_DOMAIN` | — | 根域名。必填，用于将 session cookie 共享给所有 `*.BASE_DOMAIN` 子域名（如 `traefik.BASE_DOMAIN`、各 project 子域名）。 |
| `JWT_SECRET` | — | 必须与控制平面的值一致 |
| `ADMIN_EMAILS` | — | 必须与控制平面的值一致；用于控制 `/verify-admin`（Traefik 控制台）访问权限 |

## Agent（`muvee-agent`）

| 变量 | 默认值 | 说明 |
|---|---|---|
| `NODE_ROLE` | _（必填）_ | `builder` 或 `deploy` |
| `CONTROL_PLANE_URL` | `http://localhost:8080` | 控制平面的**内网**地址（如 `http://10.0.0.1:8080`）。不要使用公开域名——详见 [Agent 节点](./agents)。 |
| `AGENT_SECRET` | — | 必须与控制平面上设置的值一致 |
| `DATA_DIR` | `/muvee/data` | 本地数据集缓存根目录（部署节点使用） |
| `HOST_IP` | _（自动检测）_ | Traefik 用来访问此节点上容器的 IP 地址。自动从到达 `CONTROL_PLANE_URL` 所使用的网络接口检测。若自动检测结果有误（如多网卡主机），可手动覆盖。 |

:::info 镜像仓库凭据和 BASE_DOMAIN 自动下发
Agent 在启动时通过 `GET /api/agent/config` 从控制平面获取 `REGISTRY_ADDR`、`REGISTRY_USER`、`REGISTRY_PASSWORD` 和 `BASE_DOMAIN`。这些配置只需在控制平面上设置一次——无需在各个 Agent 节点上单独配置。
:::
