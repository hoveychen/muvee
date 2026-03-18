---
id: install-agent
title: Agent 安装指引
sidebar_position: 9
---

# Agent 安装指引

muvee agent 运行在工作节点上，通过轮询控制平面获取构建或部署任务。本文从零开始，手把手带你完成依赖安装到节点注册的全过程，覆盖 Linux、macOS 和 Windows。

## 开始之前

你需要从控制平面获取以下信息：

| 值 | 获取位置 |
|-------|-----------------|
| `CONTROL_PLANE_URL` | 控制平面的内网地址，例如 `http://10.0.0.1:8080` |
| `AGENT_SECRET` | 控制平面 `.env` 中配置的值 |
| `NODE_ROLE` | `builder` 或 `deploy` |

:::caution 使用内网地址
`CONTROL_PLANE_URL` 必须设置为控制平面的**内网地址**，而非对外公开的域名。Agent 通过该地址自动检测自己的 `HOST_IP`，供 Traefik 将流量路由回部署的容器。
:::

---

## Linux

### 安装依赖

Agent 需要 **Docker CE 20.10+**（含 `docker-buildx-plugin`）、`git` 和 `rsync`。**不要使用** Ubuntu 默认 apt 源中的 `docker.io` 包——该包通常不含 buildx 插件，会导致 Builder 节点报错 `unknown flag: --platform`。

**从 Docker 官方源安装 Docker CE（推荐）：**

```bash
# 卸载旧版本（如有）
sudo apt-get remove -y docker docker-engine docker.io containerd runc 2>/dev/null || true

# 添加 Docker 官方 GPG key 和 apt 源
sudo apt-get update
sudo apt-get install -y ca-certificates curl
sudo install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg \
  | sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
sudo chmod a+r /etc/apt/keyrings/docker.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] \
  https://download.docker.com/linux/ubuntu \
  $(. /etc/os-release && echo "$VERSION_CODENAME") stable" \
  | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null

# 安装 Docker CE 及 buildx 插件
sudo apt-get update
sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin git rsync

sudo systemctl enable --now docker
sudo usermod -aG docker $USER
newgrp docker
```

:::note Debian
将上述命令中的 `ubuntu` 替换为 `debian`，其余步骤相同。
:::

**启动 Agent 前先验证安装：**

```bash
# Docker 版本须 ≥ 20.10
docker version --format '{{.Server.Version}}'

# buildx 必须可用，且支持 --platform
docker buildx version
docker buildx build --help | grep -- --platform

# git 和 rsync
git --version
rsync --version
```

如果 `docker buildx version` 报错，或者 `--platform` 参数不存在，说明 buildx 插件缺失或 Docker 版本过旧。请使用上面的官方源重新安装。

### 方式 A — Docker（Linux 推荐）

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
  -v /mnt/nfs/volumes:/mnt/nfs/volumes \
  -v /mnt/nfs/datasets:/mnt/nfs/datasets \
  ghcr.io/hoveychen/muvee:latest agent
```

容器挂载宿主机的 `/var/run/docker.sock`，让 Agent 可以控制**宿主机** Docker 守护进程（Docker-outside-of-Docker）。部署出的容器是 Agent 容器的兄弟，而非嵌套在其内部。

### 方式 B — 二进制

```bash
curl -Lo muvee https://github.com/hoveychen/muvee/releases/latest/download/muvee_linux_amd64
chmod +x muvee && sudo mv muvee /usr/local/bin/

NODE_ROLE=deploy \
  CONTROL_PLANE_URL=http://10.0.0.1:8080 \
  AGENT_SECRET=<your-agent-secret> \
  DATA_DIR=/muvee/data \
  muvee agent
```

以 systemd 服务运行：

```ini title="/etc/systemd/system/muvee-agent.service"
[Unit]
Description=muvee agent
After=network.target docker.service
Requires=docker.service

[Service]
Environment=NODE_ROLE=deploy
Environment=CONTROL_PLANE_URL=http://10.0.0.1:8080
Environment=AGENT_SECRET=REPLACE_WITH_SECRET
Environment=DATA_DIR=/muvee/data
ExecStart=/usr/local/bin/muvee agent
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now muvee-agent
```

---

## macOS

macOS 上的 Docker Desktop 将容器运行在 Linux 虚拟机内，容器里的 `docker.sock` 指向虚拟机的 Docker 守护进程，而非宿主机。因此部署的容器无法被 Traefik 访问，建议使用**原生二进制**。

### 安装依赖

```bash
# 安装 Homebrew（如未安装）
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

# Docker Desktop — 提供 docker CLI 和 buildx
brew install --cask docker
open /Applications/Docker.app   # 完成向导后验证：
docker version

# git 和 rsync
xcode-select --install   # rsync 已预装，git 随 Xcode CLT 一起安装
```

### 下载二进制

```bash
# Apple Silicon (M1 / M2 / M3)
curl -Lo muvee https://github.com/hoveychen/muvee/releases/latest/download/muvee_darwin_arm64
chmod +x muvee && sudo mv muvee /usr/local/bin/

# Intel Mac
curl -Lo muvee https://github.com/hoveychen/muvee/releases/latest/download/muvee_darwin_amd64
chmod +x muvee && sudo mv muvee /usr/local/bin/
```

### 启动 Agent

```bash
# Builder 节点
NODE_ROLE=builder \
  CONTROL_PLANE_URL=http://10.0.0.1:8080 \
  AGENT_SECRET=<your-agent-secret> \
  muvee agent

# Deploy 节点
NODE_ROLE=deploy \
  CONTROL_PLANE_URL=http://10.0.0.1:8080 \
  AGENT_SECRET=<your-agent-secret> \
  DATA_DIR=/Users/Shared/muvee/data \
  muvee agent
```

### 以后台服务运行（launchd）

```xml title="/Library/LaunchDaemons/com.muvee.agent.plist"
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.muvee.agent</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/muvee</string>
    <string>agent</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>NODE_ROLE</key><string>deploy</string>
    <key>CONTROL_PLANE_URL</key><string>http://10.0.0.1:8080</string>
    <key>AGENT_SECRET</key><string>REPLACE_WITH_SECRET</string>
    <key>DATA_DIR</key><string>/Users/Shared/muvee/data</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/var/log/muvee-agent.log</string>
  <key>StandardErrorPath</key><string>/var/log/muvee-agent.log</string>
</dict>
</plist>
```

```bash
sudo launchctl load /Library/LaunchDaemons/com.muvee.agent.plist
```

---

## Windows

推荐在 **WSL2** 中运行 Agent，可获得完整的 Linux 环境并访问 Docker Desktop 守护进程。

### 第 1 步 — 安装 WSL2

以管理员身份打开 PowerShell：

```powershell
wsl --install
```

这会安装 WSL2 和 Ubuntu，按提示重启。

### 第 2 步 — 安装 Docker Desktop

1. 下载并安装 [Docker Desktop for Windows](https://www.docker.com/products/docker-desktop/)。
2. Docker Desktop → **Settings → General**：启用 **Use the WSL 2 based engine**。
3. Docker Desktop → **Settings → Resources → WSL Integration**：启用你的 Ubuntu 发行版。

在 WSL2 终端中验证：

```bash
docker version
```

### 第 3 步 — 安装 WSL2 内的依赖

```bash
sudo apt-get update
sudo apt-get install -y git rsync
# docker CLI 由 Docker Desktop 通过 WSL 集成提供
```

### 第 4 步 — 下载二进制

```bash
curl -Lo muvee https://github.com/hoveychen/muvee/releases/latest/download/muvee_linux_amd64
chmod +x muvee && sudo mv muvee /usr/local/bin/
```

### 第 5 步 — 启动 Agent

```bash
# Deploy 节点（WSL2 内）
NODE_ROLE=deploy \
  CONTROL_PLANE_URL=http://10.0.0.1:8080 \
  AGENT_SECRET=<your-agent-secret> \
  DATA_DIR=/mnt/c/muvee/data \
  muvee agent
```

Windows 路径在 WSL2 内通过 `/mnt/c/...` 访问。

### 第 6 步 — 设置为后台服务

在 WSL2 内编辑 `/etc/wsl.conf`：

```ini title="/etc/wsl.conf"
[boot]
command = service muvee-agent start
```

创建 init 脚本：

```bash
sudo tee /etc/init.d/muvee-agent > /dev/null <<'EOF'
#!/bin/sh
export NODE_ROLE=deploy
export CONTROL_PLANE_URL=http://10.0.0.1:8080
export AGENT_SECRET=REPLACE_WITH_SECRET
export DATA_DIR=/mnt/c/muvee/data
exec /usr/local/bin/muvee agent >> /var/log/muvee-agent.log 2>&1
EOF
sudo chmod +x /etc/init.d/muvee-agent
sudo update-rc.d muvee-agent defaults
```

重启 WSL2 使配置生效：`wsl --shutdown`，然后重新打开终端。

---

## 验证注册

启动 Agent 后，确认它出现在控制平面中：

```bash
muveectl nodes list
```

节点应在 30 秒内显示为 **online**。如果一直显示 offline：

1. 验证 `CONTROL_PLANE_URL` 可达：`curl http://10.0.0.1:8080/healthz`
2. 验证 `AGENT_SECRET` 与控制平面 `.env` 一致
3. 验证 Docker 正在运行：`docker version`
4. 查看 Agent 日志排查错误

节点注册后，也可在 WebUI 的 **Nodes 页面**查看每个节点的实时 health check 结果（包括 `docker_buildx` 检测状态）。

---

## 环境变量参考

| 变量 | 必填 | 默认值 | 说明 |
|----------|----------|---------|-------------|
| `NODE_ROLE` | ✓ | — | `builder` 或 `deploy` |
| `CONTROL_PLANE_URL` | ✓ | `http://localhost:8080` | 控制平面的内网地址 |
| `AGENT_SECRET` | ✓ | — | 共享密钥（须与控制平面一致） |
| `DATA_DIR` | 仅 deploy | `/muvee/data` | 数据集本地缓存目录 |
| `HOST_IP` | — | 自动检测 | 覆盖上报给 Traefik 的 IP |

---

## 各角色前置要求

### Builder 节点

| 要求 | 最低版本 | 验证命令 |
|---|---|---|
| Docker CE | 20.10+ | `docker version --format '{{.Server.Version}}'` |
| docker-buildx-plugin | 0.9+ | `docker buildx version` |
| `git` | 任意近期版本 | `git --version` |

Builder 会执行 `docker buildx build --platform linux/amd64 --push` 来构建并推送镜像。`buildx` 子命令和 `--platform` 参数均为必需，由 Docker 官方源的 `docker-buildx-plugin` 包提供——**不是** Ubuntu 默认 apt 源里的 `docker.io` 包。

:::caution 常见故障
如果 Builder 节点日志出现 `unknown flag: --platform` 或 `docker: 'buildx' is not a docker command`，说明 `docker-buildx-plugin` 缺失或 Docker 版本过旧。请按照上方官方源安装步骤重装，然后重启 Agent。

也可在管理界面查看节点的实时 health check：**Settings → Nodes → `docker_buildx`**。
:::

### Deploy 节点

| 要求 | 验证命令 |
|---|---|
| Docker（任意近期版本） | `docker version` |
| `rsync` | `rsync --version` |
| NFS 挂载于 `DATASET_NFS_BASE_PATH` | `ls $DATASET_NFS_BASE_PATH` |
