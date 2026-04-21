---
id: secrets
title: 密钥管理
sidebar_position: 4
---

# 密钥管理

Muvee 内置密钥（Secrets）存储，用于安全管理密码、API 令牌和 SSH 私钥。密钥在数据库中以 AES-256-GCM 加密存储，并在部署时注入到运行环境中。

## 工作原理

```
用户创建密钥 → 加密后存入数据库（AES-256-GCM）
       ↓
用户将密钥绑定到项目（env_var_name, use_for_git, use_for_build, build_secret_id）
       ↓
部署时，调度器解密密钥并：
  • SSH 密钥（use_for_git=true） → 构建节点用于 git clone
  • use_for_build=true 且配置了 build_secret_id 的密钥 → 构建节点通过 docker buildx --secret 注入
  • 设置了 env_var_name 的密钥 → 以 docker run -e KEY=VALUE 注入
```

密钥是**只写的**——创建后无法再次查看其值。如需轮换密钥，请删除旧密钥，以相同名称创建新密钥，然后重新绑定到项目。

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

:::warning
`env_var` 仅适用于可在 UI 中公开展示的值。敏感内容请使用 `password` 或 `api_key`。
:::

## 在 UI 中管理密钥

在侧边栏导航到 **Secrets**（密钥），可以：

- 查看所有密钥（显示名称、类型；`api_key` / `env_var` 还会显示预览片段）
- 创建新密钥（上述四种类型之一）
- 删除密钥

## 将密钥绑定到项目

打开项目并点击 **Secrets** 标签页，可以：

- 为项目附加 / 解除密钥绑定
- 设置每个密钥注入时使用的**环境变量名**（如 `GITHUB_TOKEN`、`DATABASE_PASSWORD`）
- 对于 SSH 密钥类型，启用 **"Use for git clone"**（用于 git 克隆）——构建节点在克隆 git 仓库时将使用该密钥
- 启用 **"Use for docker build secret"** 并设置 **Build Secret ID**（如 `github_token`），即可在 Dockerfile 构建阶段通过 `/run/secrets/github_token` 读取

:::note
环境变量注入将在**下次部署**时生效。更新密钥绑定后，请重新部署项目。
:::

## 通过命令行管理密钥

### 密钥操作

```bash
# 列出密钥（值永远不会返回）
muveectl secrets list

# 创建密码类型密钥
muveectl secrets create --name GITHUB_TOKEN --type password --value ghp_xxxxx

# 从文件创建 SSH 密钥
muveectl secrets create --name DEPLOY_KEY --type ssh_key --value-file ~/.ssh/id_ed25519

# 删除密钥
muveectl secrets delete SECRET_ID
```

### 项目绑定操作

```bash
# 列出绑定到项目的密钥
muveectl projects secrets PROJECT_ID

# 将密钥作为环境变量绑定
muveectl projects bind-secret PROJECT_ID \
  --secret-id SECRET_ID \
  --env-var GITHUB_TOKEN

# 将 SSH 密钥绑定用于 git clone
muveectl projects bind-secret PROJECT_ID \
  --secret-id SSH_KEY_SECRET_ID \
  --use-for-git

# 将密钥绑定为 docker buildx 构建密钥
muveectl projects bind-secret PROJECT_ID \
  --secret-id SECRET_ID \
  --use-for-build \
  --build-secret-id github_token

# 移除密钥绑定
muveectl projects unbind-secret PROJECT_ID SECRET_ID
```

## 私有 Git 仓库工作流

从私有 GitHub / GitLab 仓库部署的步骤：

1. 生成 SSH 密钥对：
   ```bash
   ssh-keygen -t ed25519 -f deploy_key -N ""
   ```
2. 将**公钥**（`deploy_key.pub`）添加为仓库的 Deploy Key。
3. 在 Muvee 中使用**私钥**创建 SSH 密钥类型的密钥：
   ```bash
   muveectl secrets create --name MY_DEPLOY_KEY --type ssh_key --value-file deploy_key
   ```
4. 在项目的 **Secrets** 标签页中，附加该密钥并启用 **"Use for git clone"**。
5. 触发新的部署——构建节点将自动使用该密钥。

## 安全说明

- 密钥值在存入数据库前以 **AES-256-GCM** 加密。
- 解密后的值会包含在控制平面发送给 Agent 节点的任务载荷中，通过内网传输。请确保该网络是受信任的。
- 密钥归属于**创建它的用户**。其他用户无法查看或使用你的密钥，除非你授权共享。

## 构建阶段密钥示例（私有 Go 依赖）

当你的仓库需要拉取私有 Go module 时，可以把 PAT 密钥绑定为构建密钥：

```bash
# 1）创建 PAT 密钥
muveectl secrets create --name GITHUB_TOKEN --type password --value github_pat_xxxx

# 2）绑定为 docker build secret
muveectl projects bind-secret PROJECT_ID \
  --secret-id SECRET_ID \
  --use-for-build \
  --build-secret-id github_token

# 3）触发部署
muveectl projects deploy PROJECT_ID
```

Dockerfile 示例：

```dockerfile
# syntax=docker/dockerfile:1.7
RUN --mount=type=secret,id=github_token \
    TOKEN="$(cat /run/secrets/github_token)" && \
    git config --global url."https://x-access-token:${TOKEN}@github.com/".insteadOf "https://github.com/" && \
    GOPRIVATE=github.com/your-org/* GONOSUMDB=github.com/your-org/* \
    go mod download
```
