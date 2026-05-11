# SPA Auth SDK — Design Doc

> 草稿状态，等老板过目后拆 TASKS.md。文件放在 `docs/` 根（非 `docs/docs/`），不进 docusaurus 站点。

## 0. 背景与决策（2026-05-11 拍板）

| 决策点 | 选项 |
|---|---|
| Provider 存储 | `projects.enabled_providers` 列（逗号分隔，空 = 沿用全局启用集）|
| 登陆通信机制 | **Polled login_token**（统一覆盖 web/desktop/mobile，零跨域）|
| State cookie | HMAC 签名，载荷含 mode + return_to |
| SDK 仓库位置 | `sdk/auth-ts/`（独立 npm 包，**非 monorepo**）|
| Migration 编号 | 037（最新 036，034 缺号保留给 033 follow-up）|
| Device flow 内宿主行为 | SDK 仅返回 `oauth_url` + 轮询 token，宿主自己决定怎么打开（new tab / popup / 外部浏览器）|
| npm 包名 | `@muvee/auth`（备选 `muvee-auth`，scope 占用待 publish 时确认）|
| 桌面端 bearer token | **不返**。SDK 面向 project 下游用户，这群人与 muvee 平台用户不是同一群，无需调 muvee 平台 API |
| login_token TTL / poll interval | 10 min / 2s |
| 实现节奏 | design doc 过目 → 拆 TASKS.md → 实现 |

## 1. 现状速览

### 1.1 两套 OAuth 链路

- **平台登陆**：`/auth/{provider}/callback` → `auth.Service.HandleCallback()` → `EnsurePlatformMember()` 强制 `ALLOWED_DOMAINS` + invite/access-mode gate；写 `users` + `platform_members`。
- **项目子域登陆**：`/_oauth/{provider}` → `handleOAuthCallback()` → `upsertUserUpstream()`（绕过平台策略），写 `users`；session 是 `muvee_fwd_session` JWT cookie，cookie 域 = `cookieDomain`（BASE_DOMAIN）。
- 两套链路**共用同一份** OAuth provider 配置。

### 1.2 已经能复用的资产

- `/_oauth/userinfo`（[authservice.go:293](../cmd/muvee/authservice.go#L293)）支持 BASE_DOMAIN 子域 cross-origin + credentials。
- `/_oauth/logout`（[authservice.go:346](../cmd/muvee/authservice.go#L346)）已存在。
- Device flow（`/_oauth/device/code` + `/activate` + `/token`）有完整实现 + 内存 `deviceFlows` map，**新设计直接演进它**，下文细说。
- Traefik 在每个 project 子域把 `/_oauth/*` 路由到 authservice 且绕过 ForwardAuth（[server.go:3231-3238](../internal/api/server.go#L3231-L3238)）。

### 1.3 已知不一致（不在本次范围）

子域登陆**不查 domain**，session 永远会建；访问受限 project 时才在 `/verify` 阶段 403。沿用现状，老板说后面单独提需求。

## 2. 数据库变更

新增 migration `db/migrations/0XX_project_enabled_providers.sql`（号待选）：

```sql
ALTER TABLE projects
  ADD COLUMN enabled_providers TEXT NOT NULL DEFAULT '';
```

- 空字符串 = **沿用全局所有已启用的 provider**（向后兼容老 project，零 backfill）。
- 非空 = 逗号分隔白名单，如 `"google,feishu"`。
- 写入校验：name 必须在 `internal/auth.Service.providers` map 内。
- `internal/store/models.go` Project 结构同步加 `EnabledProviders string`。

## 3. 后端 API 设计

**核心思路**（老板拍板）：所有登陆方式（web SPA / Tauri / RN / Electron / CLI）统一走「申请 login_token → 用户在任意窗口完成 OAuth → SPA 轮询 login_token」。**没有 postMessage、没有 popup origin 校验、没有 mode 分支**。窗口关掉/后退即可，轮询自己会发现成功。

这本质是把现有 device flow 的「输 user_code」环节短路掉 —— OAuth URL 里直接嵌 state 关联到 login_token，用户不再需要拼对人工 code。

### 3.1 新增 `GET /_oauth/providers`

返回当前 host 对应 project **实际启用的** provider 列表，供 SPA 渲染按钮：

```json
[
  { "name": "google", "display_name": "Google" },
  { "name": "feishu", "display_name": "Feishu" }
]
```

- 公开端点。`applyUserInfoCORS` 让 SPA 跨域可调。
- 解析 Host → projectID → 计算 `enabled_providers ∩ 全局已注册`。空则退回全局集合。

### 3.2 新增 `POST /_oauth/login-token`

SDK 申请一次登陆会话。

请求：
```json
{ "provider": "google" }
```

响应：
```json
{
  "login_token": "<opaque-64-bytes>",
  "oauth_url":   "https://accounts.google.com/o/oauth2/v2/auth?...&state=...",
  "expires_in":  600,
  "poll_interval": 2
}
```

逻辑：
1. 解析 Host → projectID；校验 `provider ∈ enabled set`，否则 400。
2. 在 authservice 进程内 sync.Map 里新建 `loginTokenEntry{ Provider, ProjectID, ExpiresAt, Status:"pending" }`，map key 是 `login_token`。
3. `state = HMAC-signed("login-token:<login_token>:<nonce>")`，写 server-side 映射 `state → login_token`（复用现有 `devicePending` 思路）。**不再依赖** `fwd_oauth_state` cookie（因为发起方和 callback 方可能不是同一个浏览上下文，例如桌面端打外部浏览器）。
4. `oauth_url = provider.AuthCodeURL(state)`，原样返回给 SDK。

> 安全：login_token 仅 SDK 知道（在 ESM 内存里），oauth_url 里暴露的是 state。攻击者拿到 oauth_url 也只能替已申请的 login_token 完成登陆 —— 攻击者拿不到 login_token 就无法 poll 拿身份。

### 3.3 新增 `POST /_oauth/login-token/poll`

SDK 轮询。

请求：
```json
{ "login_token": "..." }
```

响应（任选其一）：
- `{ "status": "pending" }`
- `{ "status": "success", "user": { "email": "...", "name": "...", "avatar_url": "...", "provider": "..." } }`
- `{ "status": "expired" }`
- `{ "status": "error", "error": "user_cancelled" }`

成功后立刻删 map 项（一次性消费）。SDK 拿到 success 后调 `/_oauth/userinfo`（带 cookie）确认 session 同时拿到的 user 来源一致 —— 注意：success 时 `muvee_fwd_session` cookie 已经在 callback 阶段写好了（前提：callback 和 SPA 在同一浏览器、同一 cookie 域；桌面端走外部浏览器时 cookie 写不到 app 进程，**桌面端 SDK 不依赖 cookie**，直接相信 poll 返回的 user 字段）。

### 3.4 改造 `handleOAuthCallback`

callback 时按 state 解析分支：

| state 形态 | 行为 |
|---|---|
| 旧 `device-...`（input code 路径）| 走现有 device flow 逻辑，**保留** |
| 新 `login-token-...`（来自 3.2）| 标记对应 login_token 为 success；**仍然写** `muvee_fwd_session` cookie；返回一个静态完成页（「✓ 已登陆，可关闭此窗口」，自动 1s 后 `window.close()`）|
| 旧 ForwardAuth 触发的纯 redirect 流程 | 保留现状（写 cookie，302 到 `fwd_oauth_redirect`）|

**完成页面**复用现有 device flow 那段 `<h2>✓ Device authorized</h2>` HTML（[authservice.go:484-489](../cmd/muvee/authservice.go#L484-L489)），文案改成「Login complete — you can close this window」。

### 3.5 enabled_providers 强制校验点

- `POST /_oauth/login-token`（主入口）
- `/_oauth/login`（既有 ForwardAuth 登陆页按 enabled 集过滤选项）
- `/_oauth/device/activate`
- `/_oauth/{provider}` callback —— 二次校验（防 state 伪造或配置变更竞态）

### 3.6 平台 admin API

`internal/api/server.go` 的 `CreateProject` / `UpdateProject`：
- 接收 `enabled_providers` 字段
- 校验每个值在 `auth.Service.ListProviders()` 内
- `GET /api/projects/{id}` 返回字段

## 4. TypeScript SDK 设计

### 4.1 包与位置

- 仓库路径：`sdk/auth-ts/`（**独立子项目**，独立 `package.json`、独立 `tsconfig.json`、独立 release 流程）
- 包名候选：`@muvee/auth`（待 npm 可用性确认）
- 产物：ESM + CJS + `.d.ts`，零运行时依赖
- 不进 web/ 的 React 项目，web admin UI 要用就装 npm 包或 file: 链接

### 4.2 公开 API（精简版）

```ts
export interface MuveeAuthConfig {
  /** 项目子域根 URL，例如 "https://my-app.example.com"。默认 window.location.origin */
  baseUrl?: string;
  /** 轮询间隔下限秒数，默认从 server 返回的 poll_interval 取 */
  pollInterval?: number;
}

export interface MuveeUser {
  email: string;
  name: string;
  avatar_url: string;
  provider: string;
}

export interface ProviderInfo {
  name: string;
  display_name: string;
}

export interface SignInHandle {
  /** 后端返回的授权 URL —— 宿主决定怎么打开（新 tab / popup / Tauri shell open / RN Linking） */
  oauthUrl: string;
  /** 等待用户完成登陆，resolve 用户身份，reject expired/cancelled */
  wait(): Promise<MuveeUser>;
  /** SPA 取消登陆，停止轮询 */
  cancel(): void;
}

export class MuveeAuth {
  static configure(config: MuveeAuthConfig): void;

  static listProviders(): Promise<ProviderInfo[]>;

  /**
   * 发起登陆：申请 login_token，返回 oauth_url 给宿主打开 + 一个 wait() promise。
   * 宿主负责打开 oauth_url（window.open / location.href / Tauri shell / RN Linking），
   * SDK 在后台轮询 login_token。
   */
  static signIn(provider: string): Promise<SignInHandle>;

  static signOut(opts?: { redirect?: string }): Promise<void>;

  static getUser(): Promise<MuveeUser | null>;

  /** 跨 tab session 变更通知（BroadcastChannel + storage 事件兜底）*/
  static onAuthChange(cb: (u: MuveeUser | null) => void): () => void;
}
```

> 关键：SDK **不**自动开窗口 —— 宿主拿到 `oauthUrl` 后自己决定。Web 通常 `window.open(oauthUrl, '_blank')` 或 `location.href = oauthUrl`；Tauri 调 `@tauri-apps/api/shell.open`；RN 调 `Linking.openURL`。这是老板明确要求的，SDK 不绑定环境 API。

### 4.3 典型 web 用法

```ts
async function handleLogin(provider: string) {
  const handle = await MuveeAuth.signIn(provider);
  const win = window.open(handle.oauthUrl, '_blank');
  try {
    const user = await handle.wait();   // 用户登陆完关掉新 tab，poll 完成
    console.log('signed in as', user);
  } catch (e) {
    handle.cancel();
  }
}
```

### 4.4 Tauri 用法

```ts
import { open } from '@tauri-apps/api/shell';

const handle = await MuveeAuth.signIn('google');
await open(handle.oauthUrl);    // 默认浏览器打开
const user = await handle.wait();
```

## 5. 下游应用读取 user 的协议（不变）

| 场景 | 怎么读 |
|---|---|
| 服务端代码 | Traefik 注入的 `X-Forwarded-User` / `X-Forwarded-Email` header |
| SPA 前端 | `GET /_oauth/userinfo` with `credentials: 'include'` |
| 桌面 / RN | 从 `signIn().wait()` 直接拿 user 字段；这群下游用户不需要调 muvee 平台 API，所以不签发 bearer token |

## 6. 集成示例（待写）

文档 `docs/docs/auth/spa-auth-sdk.md`，给 React / Vue / Tauri / RN 各一段。

## 7. Admin UI

`web/src/pages/ProjectDetail.tsx`：
- 「Access control」区块下方加「Sign-in providers」
- 多选 checkbox 列全局已注册 provider
- 全勾 = 落库空字符串（向后兼容）；勾部分 = 逗号分隔
- 文案：「Empty = all globally enabled providers」

## 8. 安全 & 风险

| 风险 | 对策 |
|---|---|
| login_token 泄露被人替换登陆 | login_token 仅 SDK 持有（POST 响应，不落 cookie / URL）；TTL 默认 10 min；一次性消费；同 token 重复 poll 拿到 success 后立即失效 |
| state 伪造 | HMAC 签名 + server 端 `state → login_token` 内存映射双重校验 |
| oauth_url 被泄漏给第三方 | 即使泄漏，第三方完成 OAuth 也只能填进**已申请**的 login_token；攻击者没 login_token 拿不到结果。这等价于 device flow 本身的威胁模型 |
| enabled_providers 改了但已有 oauth_url 在飞 | callback 阶段（3.4）二次校验 provider ∈ enabled set，拒绝 |
| 多 tab signOut 不同步 | BroadcastChannel + storage event |
| 轮询过频拖垮 authservice | server 返回 `poll_interval`（默认 2s），SDK 必须遵守；429 时退避 |

## 9. TASKS.md 草稿（待老板过目后落地）

> Migration 编号在跑 P1 前 `ls db/migrations/` 重新选。

- **P1** — Migration `0XX_project_enabled_providers.sql` + `Project.EnabledProviders` 字段 + store 读写 + admin API 读写校验
- **P2** — `GET /_oauth/providers` 端点 + Host→Project 解析 helper 复用
- **P3** — HMAC-signed state 工具函数（在 `internal/auth/` 或 `cmd/muvee/authservice.go` 新文件 `state.go`）
- **P4** — `POST /_oauth/login-token` + 内存 `loginTokens` map + 过期 GC
- **P5** — `POST /_oauth/login-token/poll`
- **P6** — `handleOAuthCallback` 加 login-token state 分支 + 完成页
- **P7** — `/_oauth/login` / `/_oauth/device/activate` 按 project enabled 集过滤；callback 二次校验
- **P8** — `sdk/auth-ts/` scaffolding（tsup + package.json + tsconfig + README 框架）
- **P9** — SDK 实现 `configure` / `listProviders` / `signIn` / `signOut` / `getUser` / `onAuthChange`
- **P10** — `web/src/pages/ProjectDetail.tsx` 加 providers 选择 UI；`web/src/lib/types.ts` 同步
- **P11** — `docs/docs/auth/spa-auth-sdk.md` 集成文档（React + Tauri + RN 示例）
- **P12** — `go build ./...` + 单测（store / authservice login-token 生命周期）
- **P13** — 在 web/ 或一个独立 demo SPA 真机过 web 流程；如果方便，跑一遍 Tauri demo
- **P14** — 报告完成 + 问老板要不要实战在一个 project 上启用 SDK 验证

## 10. 待拍板细节

全部已拍板，见 §0 决策表。`@muvee` scope 占用待真正 `npm publish` 时再确认；若被占退回 `muvee-auth`。
