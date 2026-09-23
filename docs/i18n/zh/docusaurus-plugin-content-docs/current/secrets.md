---
id: secrets
title: 密钥与环境变量
sidebar_position: 4
---

# 密钥与环境变量

Muvee 内置密钥（Secrets）存储，用于安全管理密码、API 令牌和 SSH 私钥，在数据库中以 AES-256-GCM 加密存储。个人密钥本身不会生效——它只是一个来源，你可以把它**复制**到某个项目的**环境变量**中，或复制到项目的**Git 凭据**中（用于克隆私有仓库）。唯一的例外是 `registry` 类型：它从不被复制，而是自动应用到你名下的所有 compose 项目。

## 工作原理

```
用户创建密钥 → 加密后存入数据库（AES-256-GCM）
       ↓
用户将密钥复制到某个项目的环境变量，或复制到项目的 Git 凭据——
每一份拷贝都独立存储在该项目上
       ↓
部署 / 重启时：
  • 运行时变量 → 以 docker run -e KEY=VALUE 注入
  • 构建变量 → 以 --secret id=KEY 传给 docker buildx
  • 项目的 Git 凭据（https_token / ssh_key） → 构建节点用它来 clone
  • registry 密钥 → 自动应用到所有者名下的全部 compose 项目（无需复制）
```

密钥是**只写的**——创建后无法再次查看其值。由于拷贝与来源相互独立，之后删除或轮换个人密钥**不会**影响已经复制过它的项目。

## 前置条件

在创建任何密钥之前，需要在控制平面上设置 `SECRET_ENCRYPTION_KEY` 环境变量。该值必须是 **64 字符的十六进制字符串**（32 字节）：

```bash
# 生成安全密钥
openssl rand -hex 32
# 例如 a3f4e1b2c8d7...

# 在环境变量 / .env 文件中设置
SECRET_ENCRYPTION_KEY=a3f4e1b2c8d7...
```

:::caution
若未设置 `SECRET_ENCRYPTION_KEY`，密钥创建功能将被禁用。请妥善备份此密钥——一旦丢失，所有加密的密钥将无法恢复。
:::

## 密钥类型

| 类型 | 适用场景 | 展示方式 |
|---|---|---|
| `password` | API 令牌、数据库密码、通用凭据 | 只写（值永远不会回显） |
| `ssh_key` | PEM 格式 SSH 私钥，用于克隆私有 git 仓库 | 只写（值永远不会回显） |
| `api_key` | API 密钥 / 第三方 Token，借脱敏片段识别“这条是哪把钥匙” | 展示头 4 + 尾 4 位（如 `sk-1****wxyz`） |
| `env_var` | 非敏感的集中配置（公共端点、feature flag 等） | 在密钥列表中以明文完整展示 |
| `registry` | 私有容器镜像仓库（如 `ghcr.io`）的拉取凭据，供 compose 项目拉取私有镜像 | 只写（token 永远不会回显）；仓库地址与用户名会展示 |

:::warning
`env_var` 仅适用于可在 UI 中公开展示的值。敏感内容请使用 `password` 或 `api_key`。
:::

类型主要影响复制到项目时的默认敏感级别：复制 `env_var` 密钥会生成一条非敏感的项目变量，其余类型默认都是敏感的。

### 私有镜像仓库凭据

`registry` 密钥保存一份私有容器镜像仓库的登录信息。与其他类型不同，它**不会**按项目复制：你名下的每一个 compose 项目在部署时都会自动使用你**全部**的 `registry` 密钥来拉取镜像。这正是 `docker-compose` 项目拉取 `ghcr.io/your-org/your-app:latest` 这类私有镜像的方式。

密钥的**值**是仓库密码 / token；**仓库地址**（如 `ghcr.io`）与**登录用户名**随密钥一起存储。部署时 Agent 会写入一份临时的、按次部署生成的 docker 配置文件来使用这些凭据——它们不会进入 Agent 共享的 docker 配置，也不会跨租户泄露。

:::note
只有 **compose** 部署路径会使用 registry 凭据。单容器（Dockerfile / image）项目由 Muvee 自行构建并从 Muvee 自己的镜像仓库拉取，那个仓库已经完成了认证。
:::

## 在 UI 中管理密钥

在侧边栏导航到 **Secrets**（密钥），可以：

- 查看所有密钥（显示名称、类型；`api_key` / `env_var` 还会显示预览片段）
- 创建新密钥（上述五种类型之一）
- 删除密钥

## 项目环境变量

打开项目并进入 **环境变量** 标签页，管理属于该项目的变量。项目的所有成员和管理员看到的都是同一份列表。每一行包含一个 `KEY`（项目内唯一，需匹配 `^[A-Za-z_][A-Za-z0-9_]*$`）、一个值，以及三个开关：

- **敏感**（默认开启）——值只写：UI 中显示 `已设置 · N 字符`，而不是具体的值。敏感变量无法改回非敏感——请先取消设置再重新创建。
- **运行时**（默认开启）——注入到运行中容器的环境变量。
- **构建**（默认关闭）——作为 `docker buildx` 的构建密钥传入，其 id 就是该变量的 `KEY`（见下方[私有构建依赖](#私有构建依赖例如私有-go-模块)）。

点击 **从我的密钥复制**，可以把一个个人密钥复制到项目中——这份拷贝与来源相互独立，之后编辑或删除个人密钥不会影响该项目。

:::note
运行时变量的修改需要在项目**重启**或**重新部署**后才会生效；构建变量在**下一次部署**时生效。
:::

## 私有 Git 仓库凭据

Muvee 用于克隆**外部**仓库的凭据是一项独立的项目设置，位于项目 **Config**（配置）标签页的 **Git 仓库** 区块——它不是上面的环境变量之一。可选类型：

- **无**——匿名克隆（仅适用于公开仓库）。
- **HTTPS Token**——用户名（默认 `x-access-token`）+ 个人访问令牌。
- **SSH 部署密钥**——一个 SSH 私钥。

该值只写；该区块会显示当前是否已设置凭据。当你通过新建项目向导指向一个私有仓库时，该向导会直接写入这项设置。

## 通过命令行管理密钥

### 密钥操作

```bash
# 列出密钥（值永远不会返回）
muveectl secrets list

# 创建密码类型密钥
muveectl secrets create --name GITHUB_TOKEN --type password --value ghp_xxxxx

# 从文件创建 SSH 密钥
muveectl secrets create --name DEPLOY_KEY --type ssh_key --value-file ~/.ssh/id_ed25519

# 创建私有镜像仓库拉取凭据（自动应用到你名下所有 compose 项目）
muveectl secrets create --name GHCR_PULL --type registry \
  --registry-addr ghcr.io --registry-username my-gh-user --value ghp_xxxxx

# 删除密钥
muveectl secrets delete SECRET_ID
```

### 项目环境变量

```bash
# 列出项目的变量（KEY、SENSITIVE、RUNTIME、BUILD、VALUE_STATUS 等）
muveectl projects env PROJECT_ID

# 创建或更新一个变量（默认敏感）
muveectl projects env set PROJECT_ID DATABASE_URL=postgres://...
muveectl projects env set PROJECT_ID LOG_LEVEL=debug --plain

# 仅用于构建（docker build secret，不注入运行时）
muveectl projects env set PROJECT_ID NPM_TOKEN=npm_xxx --build --no-runtime

# 复制一个个人密钥（KEY 默认取密钥名；env_var 类型复制后为非敏感变量，其余类型为敏感）
muveectl projects env copy PROJECT_ID --secret-id SECRET_ID [--key GITHUB_TOKEN] [--build] [--no-runtime]

# 删除一个变量
muveectl projects env unset PROJECT_ID DATABASE_URL

# 查看*正在运行*的容器内实际生效的环境变量（形似密钥的键会被打码；加 --raw 显示原文）
muveectl projects env PROJECT_ID --live
muveectl projects env PROJECT_ID --live --raw
```

### 私有 Git 仓库凭据

```bash
# 查看当前凭据（类型、用户名、value_status）
muveectl projects git-credential PROJECT_ID

# HTTPS token（用户名默认为 x-access-token）
muveectl projects git-credential set PROJECT_ID --type https_token --value github_pat_xxxx

# 从文件设置 SSH 部署密钥
muveectl projects git-credential set PROJECT_ID --type ssh_key --value-file deploy_key

# 或复制一个个人的 password / ssh_key 密钥
muveectl projects git-credential set PROJECT_ID --from-secret-id SECRET_ID [--username oauth2]

# 移除凭据（匿名克隆）
muveectl projects git-credential clear PROJECT_ID
```

## 私有 Git 仓库工作流

Muvee 支持两种克隆私有仓库的方式，按你的 git 服务商选择。

---

### 方式 A：GitHub / GitLab 细粒度访问令牌（HTTPS）——推荐

GitHub 现在推荐使用**细粒度个人访问令牌**（PAT）而非 SSH 部署密钥来访问仓库。

1. 在 **GitHub → Settings → Developer settings → Fine-grained tokens** 中生成细粒度 PAT，
   并对目标仓库授予 **Contents: Read-only** 权限。

2. 将其设置为项目的 Git 凭据：
   ```bash
   muveectl projects git-credential set PROJECT_ID --type https_token --value github_pat_xxxx
   ```
   构建节点会在 clone 之前把仓库地址改写为 `https://x-access-token:TOKEN@github.com/...`。

   | 服务商 | `--username` 取值 |
   |---|---|
   | GitHub | `x-access-token`（默认） |
   | GitLab | `oauth2` |
   | Bitbucket | 你的 Bitbucket 用户名 |
   | Azure DevOps | `AzureDevOps` |

3. 触发部署——无需其他配置。

:::tip
你也可以把同一个 token 作为项目环境变量在运行时暴露给应用（例如用来推送镜像或调用 GitHub API）：
```bash
muveectl projects env set PROJECT_ID GITHUB_TOKEN=github_pat_xxxx
```
:::

---

### 方式 B：SSH 部署密钥

当你的服务商要求使用 SSH，或你更倾向于基于密钥的认证时，使用此方式。

1. 生成 SSH 密钥对：
   ```bash
   ssh-keygen -t ed25519 -f deploy_key -N ""
   ```
2. 将 `deploy_key.pub`（公钥）添加为仓库的 **Deploy Key**（GitHub：_Settings → Deploy keys_）。
3. 将其设置为项目的 Git 凭据：
   ```bash
   muveectl projects git-credential set PROJECT_ID --type ssh_key --value-file deploy_key
   ```
4. 触发部署——构建节点会通过 `GIT_SSH_COMMAND` 使用该密钥。

## 安全说明

- 密钥值在存入数据库前以 **AES-256-GCM** 加密。
- 解密后的值会包含在控制平面发送给 Agent 节点的任务载荷中，通过内网传输。请确保该网络是受信任的。
- 密钥归属于**创建它的用户**。其他用户无法查看或使用你的密钥，除非你授权共享；但项目的环境变量与 Git 凭据对该项目的所有成员可见。

## 私有构建依赖（例如私有 Go 模块）

当你的仓库需要拉取私有 Go module 等构建期依赖时，添加一个构建变量——它的构建密钥 id 就是该变量的 `KEY`：

```bash
# 1）添加一个仅用于构建的变量（不注入运行时）
muveectl projects env set PROJECT_ID GITHUB_TOKEN=github_pat_xxxx --build --no-runtime

# 2）触发部署
muveectl projects deploy PROJECT_ID
```

Dockerfile 示例：

```dockerfile
# syntax=docker/dockerfile:1.7
RUN --mount=type=secret,id=GITHUB_TOKEN \
    TOKEN="$(cat /run/secrets/GITHUB_TOKEN)" && \
    git config --global url."https://x-access-token:${TOKEN}@github.com/".insteadOf "https://github.com/" && \
    GOPRIVATE=github.com/your-org/* GONOSUMDB=github.com/your-org/* \
    go mod download
```

## 轮换密钥

轮换一个**个人密钥**（删除后重建，或直接创建一个新的）**不会**更新任何已经复制过它的项目——拷贝与来源是相互独立的。要把新值同步到某个项目：

- 直接在项目变量上设置新值：`muveectl projects env set PROJECT_ID KEY=新值`；或者
- 用相同的 `--key` 再次执行 `projects env copy`，用密钥的当前值覆盖项目变量。

无论哪种方式，别忘了 `muveectl projects restart PROJECT_ID`（或重新部署），**运行时**变量的修改才会到达正在运行的容器；**构建**变量在下一次部署时生效。
