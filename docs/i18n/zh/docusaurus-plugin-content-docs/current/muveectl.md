---
id: muveectl
title: muveectl 命令行工具
sidebar_position: 4
---

# muveectl – Muvee 命令行工具

`muveectl` 是 Muvee 的命令行客户端，让你无需打开 Web 界面，即可在本地机器上管理项目、数据集和 API 令牌。

## 安装

从 [Releases 页面](https://github.com/hoveychen/muvee/releases/latest)下载最新二进制文件：

**macOS（Apple Silicon）**
```bash
curl -Lo muveectl https://github.com/hoveychen/muvee/releases/latest/download/muveectl_darwin_arm64
chmod +x muveectl && sudo mv muveectl /usr/local/bin/
```

**macOS（Intel）**
```bash
curl -Lo muveectl https://github.com/hoveychen/muvee/releases/latest/download/muveectl_darwin_amd64
chmod +x muveectl && sudo mv muveectl /usr/local/bin/
```

**Linux（amd64）**
```bash
curl -Lo muveectl https://github.com/hoveychen/muvee/releases/latest/download/muveectl_linux_amd64
chmod +x muveectl && sudo mv muveectl /usr/local/bin/
```

**Windows（PowerShell）**
```powershell
Invoke-WebRequest -Uri https://github.com/hoveychen/muvee/releases/latest/download/muveectl_windows_amd64.exe -OutFile muveectl.exe
Move-Item muveectl.exe "$env:LOCALAPPDATA\Microsoft\WindowsApps\muveectl.exe"
```

## 身份验证

```bash
# 首次登录（打开浏览器进行 Google OAuth）
muveectl login --server https://www.example.com

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

## 全局参数

| 参数 | 说明 |
|------|-------------|
| `--server URL` | 仅本次调用覆盖已配置的服务器地址 |
| `--json` | 输出原始 JSON（便于管道处理） |

## Git 仓库要求

项目要成功部署，仓库必须满足以下条件：

### 构建阶段
- 可通过 HTTPS（公开仓库）或 SSH（构建节点必须有对应密钥）进行 `git clone --depth=1`
- 配置的分支必须存在（默认：`main`）
- 配置路径下必须存在 `Dockerfile`（默认：仓库根目录的 `Dockerfile`）
- 镜像必须为 **`linux/amd64`** 平台构建（`docker buildx build --platform linux/amd64`）

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
```
