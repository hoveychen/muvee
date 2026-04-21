---
id: muveectl
title: muveectl 命令行工具
sidebar_position: 4
---

# muveectl – Muvee 命令行工具

`muveectl` 是 Muvee 的命令行客户端，让你无需打开 Web 界面，即可在本地机器上管理项目、数据集和 API 令牌。

## 安装

### 一行安装（推荐，需要已运行的 hub）

你的 muvee hub 会提供一份已填好自己地址的安装脚本：

```bash
curl -fsSL https://YOUR_MUVEE_SERVER/api/install.sh | sh
```

脚本会自动识别操作系统和架构，并从 hub 下载对应的 binary。hub 构建时会把 `muveectl` 和 server 一起交叉编译并内嵌进去，因此整个安装过程不依赖外网。如果 hub 构建时没内嵌（例如本地 dev 构建），`/api/muveectl/*` 会自动 302 重定向到对应的 GitHub release 资产——对用户完全透明。

### 直接从 hub 下载

如果你想脚本化地抓取 binary（例如固定版本），可以直接访问 hub：

```bash
curl -Lo muveectl https://YOUR_MUVEE_SERVER/api/muveectl/muveectl_darwin_arm64
chmod +x muveectl && sudo mv muveectl /usr/local/bin/
```

把资产名换成你的平台：`muveectl_darwin_{arm64,amd64}`、`muveectl_linux_{amd64,arm64}` 或 `muveectl_windows_{amd64,arm64}.exe`。

### 直接从 GitHub 下载

如果还没有 hub（比如正准备在这台机器上装 hub），可以使用 [Releases 页面](https://github.com/hoveychen/muvee/releases/latest) 的官方预编译版本：

**macOS（Apple Silicon）**
```bash
muveectl_<VERSION>_darwin_arm64.tar.gz
```

**macOS（Intel）**
```bash
muveectl_<VERSION>_darwin_amd64.tar.gz
```

**Linux（amd64）**
```bash
muveectl_<VERSION>_linux_amd64.tar.gz
```

**Linux（arm64）**
```bash
muveectl_<VERSION>_linux_arm64.tar.gz
```

**Windows（PowerShell）**
```powershell
muveectl_<VERSION>_windows_amd64.zip
muveectl_<VERSION>_windows_arm64.zip
```

解压后，将可执行文件重命名为 `muveectl`（Windows 下为 `muveectl.exe`），并放到系统 `PATH` 中，例如 macOS / Linux 的 `/usr/local/bin/muveectl`。

## 身份验证

```bash
# 首次登录（打开浏览器进行 Google OAuth）
muveectl login --server https://example.com

# 验证会话
muveectl whoami
```

配置保存在 `~/.config/muveectl/config.json`。后续命令会自动使用已存储的服务器地址和令牌。

## 项目管理

```bash
muveectl projects list
muveectl projects create --name 名称 --git-url 地址 \
  [--branch 分支] [--domain 前缀] [--dockerfile 路径] \
  [--auth-required] [--auth-domains example.com,corp.com]
muveectl projects get PROJECT_ID
muveectl projects update PROJECT_ID [--branch 分支] [--auth-required] [--no-auth] [--auth-domains 域名]
muveectl projects deploy PROJECT_ID
muveectl projects deployments PROJECT_ID
muveectl projects metrics PROJECT_ID [--limit N]
muveectl projects workspace PROJECT_ID <ls|pull|push|rm> [参数...]
muveectl projects port-forward PROJECT_ID [--port 端口]
muveectl projects delete PROJECT_ID
```

### Google OAuth 保护（`--auth-required`）

启用后，Traefik 会拦截每个请求，在转发给容器之前将未认证用户重定向到 Google OAuth。

| 参数 | 说明 |
|------|-------------|
| `--auth-required` | 开启项目级 Google 认证 |
| `--no-auth` | 关闭项目级 Google 认证 |
| `--auth-domains example.com,corp.com` | 限制为特定邮箱域名（不填则允许所有 Google 账号） |

已认证用户的邮箱会通过 `X-Forwarded-User` HTTP 头转发给容器：

```python
# Python / Flask
user_email = request.headers.get("X-Forwarded-User")
```

```go
// Go
userEmail := r.Header.Get("X-Forwarded-User")
```

```typescript
// Node.js / Express
const userEmail = req.headers["x-forwarded-user"]
```

### 容器资源指标

部署 Agent 每隔约 15 秒通过 `docker stats` 采集容器的资源用量并上报给控制平面。使用 `projects metrics` 查看正在运行的容器状态：

```bash
# 显示最新一条采样及历史表格（默认：最近 60 条）
muveectl projects metrics PROJECT_ID

# 获取最多 120 条采样（约 30 分钟历史）
muveectl projects metrics PROJECT_ID --limit 120
```

每条采样包含：`cpu_percent`、`mem_usage_bytes`、`mem_limit_bytes`、`net_rx_bytes`、`net_tx_bytes`、`block_read_bytes`、`block_write_bytes` 以及 `collected_at`（Unix 时间戳）。

单次查询最多返回 1440 条历史（以 15 秒间隔计算约 6 小时）。

### 项目工作区

每个项目可以挂载一个持久化**工作区卷**——一个以 NFS 为后端的目录，会以 bind mount 的形式挂载到容器内。容器内的挂载路径通过 Web 界面的 `volume_mount_path` 字段配置（如 `/workspace`）。

控制平面提供文件管理 API，无需重新部署即可查看和传输工作区文件：

```bash
# 列出工作区根目录（或子目录）的文件
muveectl projects workspace PROJECT_ID ls
muveectl projects workspace PROJECT_ID ls some/subdir

# 将工作区中的文件下载到当前目录
muveectl projects workspace PROJECT_ID pull data/output.csv

# 下载并指定本地保存路径
muveectl projects workspace PROJECT_ID pull data/output.csv ./local_copy.csv

# 将本地文件上传到工作区根目录
muveectl projects workspace PROJECT_ID push ./model.bin

# 上传到指定子目录（目录不存在时自动创建）
muveectl projects workspace PROJECT_ID push ./model.bin --remote-path models/

# 删除文件或目录（递归删除）
muveectl projects workspace PROJECT_ID rm data/old_output.csv
muveectl projects workspace PROJECT_ID rm tmp/
```

:::info 前提条件
工作区功能需要在控制平面上配置 `VOLUME_NFS_BASE_PATH`，并在项目中设置 `volume_mount_path`。详见[配置参考](./configuration)。
:::

## 本地端口转发

将项目正在运行的容器转发到本地端口，方便本地开发调用。身份认证自动使用 CLI 当前登录的身份——容器会收到与生产环境相同的 `X-Forwarded-User` 头。

```bash
# 自动选择空闲端口
muveectl projects port-forward PROJECT_ID

# 指定本地端口
muveectl projects port-forward PROJECT_ID --port 3000
```

然后即可在本地调用项目的 API：

```bash
curl http://127.0.0.1:3000/api/some-endpoint
```

适用于本地开发时需要调用已部署项目暴露的 API，无需处理 OAuth 登录流程或 TLS 证书。

## 数据集管理

```bash
muveectl datasets list
muveectl datasets create --name 名称 --nfs-path NFS路径
muveectl datasets get DATASET_ID
muveectl datasets scan DATASET_ID
muveectl datasets delete DATASET_ID
```

## API 令牌管理

```bash
muveectl tokens list
muveectl tokens create [--name 名称]   # 令牌值仅在创建时显示一次
muveectl tokens delete TOKEN_ID
```

## 密钥管理

```bash
# 列出密钥（不会返回值）
muveectl secrets list

# 创建 password 密钥
muveectl secrets create --name GITHUB_TOKEN --type password --value github_pat_xxxx

# 创建 SSH 密钥
muveectl secrets create --name DEPLOY_KEY --type ssh_key --value-file ~/.ssh/id_ed25519

# 删除密钥
muveectl secrets delete SECRET_ID
```

### 项目密钥绑定

```bash
# 查看项目绑定
muveectl projects secrets PROJECT_ID

# 作为运行时环境变量注入
muveectl projects bind-secret PROJECT_ID \
  --secret-id SECRET_ID \
  --env-var GITHUB_TOKEN

# 用于 git clone（HTTPS token）
muveectl projects bind-secret PROJECT_ID \
  --secret-id SECRET_ID \
  --use-for-git \
  --git-username x-access-token

# 用于构建阶段 secret（docker buildx --secret）
muveectl projects bind-secret PROJECT_ID \
  --secret-id SECRET_ID \
  --use-for-build \
  --build-secret-id github_token

# --build-secret-id 可省略；省略时 muveectl 会根据密钥名自动推导
# 例如 "GITHUB_TOKEN" -> "github_token"

# 解绑
muveectl projects unbind-secret PROJECT_ID SECRET_ID
```

## 全局参数

| 参数 | 说明 |
|------|-------------|
| `--server URL` | 仅本次调用覆盖已配置的服务器地址 |
| `--json` | 输出原始 JSON（便于管道处理） |

## Git 仓库要求

项目要成功部署，仓库必须满足以下条件：

### 构建阶段
- 可通过 HTTPS（公开仓库或 token 密钥）或 SSH（SSH 密钥）进行 `git clone --depth=1`
- 配置的分支必须存在（默认：`main`）
- 配置路径下必须存在 `Dockerfile`（默认：仓库根目录的 `Dockerfile`）
- 镜像必须为 **`linux/amd64`** 平台构建（`docker buildx build --platform linux/amd64`）
- 若构建阶段需要私有依赖，可通过 `--use-for-build --build-secret-id <id>` 绑定密钥，并在 Dockerfile 中通过 `/run/secrets/<id>` 读取

### 运行阶段
- 容器必须在 **8080** 端口上提供 **HTTP** 服务——Traefik 负责 TLS 终止
- 不要在容器内启动 HTTPS
- 应用将通过 `https://<domain_prefix>.<base_domain>` 对外提供服务

### 数据集挂载

数据集以 Docker Volume 的形式注入到 `/data/<dataset_name>`：

| 模式 | 访问方式 |
|------|--------|
| `dependency` | 只读——rsync 缓存的本地副本 |
| `readwrite` | 读写——直接 NFS 挂载 |

## 典型工作流

```bash
# 1. 列出项目并获取 ID
muveectl projects list --json

# 2. 部署项目
muveectl projects deploy PROJECT_ID

# 3. 监控部署进度
muveectl projects deployments PROJECT_ID

# 4. 查看容器资源用量
muveectl projects metrics PROJECT_ID

# 5. 转发到本地端口进行开发
muveectl projects port-forward PROJECT_ID --port 3000

# 6. 下载容器产生的文件
muveectl projects workspace PROJECT_ID pull output/result.json
```
