---
id: auto-deploy
title: 自动部署
sidebar_position: 6
---

# 自动部署

muvee 可以在项目源发生变化时自动重新部署。两条独立触发路径
共同接入 `Scheduler.TriggerDeployment`：

1. **Git 触发** — 当跟踪的分支前进时触发。托管仓库通过
   `git push` 即时触发；外部仓库通过轮询触发。
2. **Image-digest 触发** — 仅 `compose` 项目可用。当
   `docker-compose.yml` 引用的任意镜像在上游发生 digest 变化
   时触发，即使 git 树没有动也会触发（watchtower 风格）。

两条触发路径都受全局开关控制，并以 goroutine 形式运行在控制
平面上。

## 在项目上启用

进入项目详情页，打开 **自动部署** 开关。该开关对 tunnel
项目隐藏；对 `domain_only` 项目会被拒绝（这类项目没有可部署
的内容）。

项目首次启用自动部署时：

- Git 触发：`last_tracked_commit_sha` 初始为空，下一次轮询
  （或下一次 push）就会触发部署以对齐状态。这是预期行为。
- Image-digest 触发（仅 compose）：watcher 的第一次 tick 会
  *仅记录* 当前 digest，不触发部署。后续 tick 才会比较并在
  digest 移动时触发。

## 全局开关与轮询间隔

两条触发路径都由 `auto_deploy_master_enabled` 控制。在管理员
设置页将其设为 `false`，可以在不丢失各项目开关状态的情况下
全局暂停自动部署。

| 设置项 | 默认 | 最小值 | 说明 |
|---|---|---|---|
| `auto_deploy_master_enabled` | `true` | — | 顶级开关 |
| `auto_deploy_poll_interval_seconds` | `60` | `10` | Git 轮询间隔（仅外部仓库） |
| `auto_deploy_image_watch_interval_seconds` | `600` | `60` | Image-digest 轮询间隔（仅 compose） |

调低 image-watch 间隔时需谨慎 — Docker Hub 匿名访问的限速是
每 IP 每 6 小时约 200 次请求。

## Git 触发详解

### 托管仓库（push 驱动）

git smart-HTTP handler 会在 `git-receive-pack` 成功后调用一个
post-receive 回调。服务端比较新的 HEAD 与
`last_tracked_commit_sha`，发生变化则调用 `TriggerDeployment`
并写回新的 SHA。

这条路径是事件驱动的：每一次 push 都会触发。如果你连续 push
11 个 commit，就会看到 11 次重新部署尝试。如果重新部署成本较
高，可以考虑先 squash 再 push。

### 外部仓库（轮询）

每隔 `auto_deploy_poll_interval_seconds`，控制平面会对每个启用
了自动部署的外部项目执行 `git ls-remote`，并把返回的 SHA 与
`last_tracked_commit_sha` 比较。SSH 与 HTTPS 鉴权方式与构建器
保持一致：

- **HTTPS + 密码型密钥** — 在执行单次 `ls-remote` 时，把 URL
  改写为 `https://<git_username>:<token>@host/...`。
- **SSH key 密钥** — 写入临时文件并通过
  `GIT_SSH_COMMAND="ssh -i <tempfile> -o StrictHostKeyChecking=no
  -o UserKnownHostsFile=/dev/null"` 注入。

每个 tick 内的外部轮询是串行的。某个项目的 `ls-remote` 慢或
卡住时，会在 30s 处超时，下一个项目继续。

## Image-digest 触发详解

Compose 部署不会产生构建出来的镜像 — 它在部署节点上执行
`docker compose pull` 拉取声明的镜像。所以即使 git 树没动，
重新部署也可能是有意义的：当依赖的镜像（`redis:7-alpine`、
`your-org/api:main` 等）在上游有了新 digest 时。

每隔 `auto_deploy_image_watch_interval_seconds`，watcher：

1. 列出所有 `auto_deploy_enabled = TRUE` 的 compose 项目。
2. 在跟踪分支末端拉取项目的 `compose_file_path`：
   - **托管** — 对磁盘上的裸仓库执行
     `git show refs/heads/<branch>:<compose_file_path>`。
   - **外部** — 浅克隆仓库到临时目录，读取文件，再清理临时
     目录。
3. 从 YAML 解析 `services.*.image` 字符串。包含 `${VAR}` 或
   `$VAR` 插值的字符串会被跳过（INFO 级别日志）。compose 转义
   `$$` 会被当作字面量 `$` 处理，不视为变量。
4. 通过 OCI registry HTTP API
   （`go-containerregistry/pkg/crane`）解析每个镜像的 manifest
   digest。鉴权按 host 选择：
   - 镜像 host 与 `REGISTRY_ADDR` 匹配 → 使用
     `REGISTRY_USER` / `REGISTRY_PASSWORD` 鉴权。
   - 其余 → 匿名访问。这适用于 Docker Hub、ghcr.io 公开镜像
     等。其它私有 registry 当前版本不支持（这与 compose 部署
     本身的限制一致）。
5. 与 `last_tracked_image_digests`（项目行上的 JSON map）比较。
   如果任意镜像的 digest 移动了，调用 `TriggerDeployment` 并
   写入新的 map。compose 文件里新出现的镜像（即用户新加了一个
   service）只记录、不触发 — 那是 git 触发的责任，因为编辑
   compose 文件总会落成一次 commit。

每个镜像的 digest 查询超时为 30s。一次失败不会清空之前记录的
digest，所以瞬时的 registry 抖动不会在下一个 tick 误触发重新
部署。

## Registry 连通性要求

image-digest watcher 需要 **控制平面本身** 能访问到 registry。
这相对原本"控制平面从不直接联系 registry"的约束是一个小的
放宽 — agent 仍然承担所有 `docker push` / `docker pull` 工作，
但 watcher 现在也会从控制平面发出
`HEAD /v2/<repo>/manifests/<tag>` 请求。

启动时 watcher 会探测 `https://<REGISTRY_ADDR>/v2/`：

- HTTP `200` 或 `401` → registry 可达，watcher 启动。
- 其它情况（DNS 失败、连接被拒、超时、5xx）→ watcher 打印
  warning 并干净退出。服务器其余部分不受影响；git 触发依然
  正常工作。

如果你把控制平面与 registry 部署在控制平面无法访问 registry
的网络拓扑里，image-digest 触发会被静默禁用。git 触发仍然
有效。

## Compose 特有行为

`compose` 项目在首次部署时被钉到一个固定的部署节点，以保证
docker named volume 在多次重新部署间保留。如果该节点离线，
两条触发路径上的自动部署都会以 `refusing to migrate` 失败，
被记录但不会打断其它项目的 tick。请关注项目部署列表里的连续
失败。

`expose_service` 与 `expose_port` 必须在项目上声明，自动部署
才能产生有效任务 — 否则 `dispatchComposeDeploy` 会拒绝。

## 当前 *尚未* 覆盖的场景

- 其它私有 registry（带 PAT 的 ghcr.io、ECR、阿里云 ACR
  等）。image-digest watcher 会回退为匿名访问，对这些 registry
  保护的镜像会失败收场。
- push 触发的项目级限流（一次 50 个 commit 的 push 会触发 50
  次重新部署；轮询路径天然去重）。
- 外部仓库的并行轮询（每个 tick 内项目串行检查）。

这些都不复杂，等具体场景出现时再加即可。
