---
id: spa-auth-sdk
title: SPA 鉴权 SDK
sidebar_position: 5
---

# SPA 鉴权 SDK（`@muvee/auth`）

面向 **项目下游用户** 的登录 SDK —— 也就是使用你在 muvee 上部署的应用的人，而**不是**管理 muvee 平台本身的人。`@muvee/auth` 在浏览器里运行：`signIn()` 用弹窗打开 muvee 的标准登录流程，弹窗给项目种下会话 cookie 后 `wait()` 才 resolve。SDK 全程不接触任何凭证。`baseUrl` 必须与调用 SDK 的页面同源（默认的 `window.location.origin` 即可）：弹窗完成通知、跨 tab 同步和基于 cookie 的 `getUser()` 都依赖同源，因此不能让另一个站点的页面去连远端项目。

## 工作原理

1. **`MuveeAuth.signIn(provider)`** 同步地打开弹窗 `{baseUrl}/_oauth/login?provider=<p>&popup=1`，并返回一个 `SignInHandle`。请在点击事件处理器里直接调用（前面不能有 `await`），否则弹窗会被浏览器拦截。
2. **用户在弹窗里完成登录**。由于 `signIn` 总是带 `provider=`，弹窗经平台中心登录主机直接进入所选 provider：不会显示项目登录页，也不会先尝试平台静默登录。用户每次都会经过 provider（provider 自身仍有会话时，可能无需输入任何内容）。
3. **弹窗最终落在 `/_oauth/popup-done`**，该页面通过 `postMessage`（目标 origin 固定为项目自身 origin）和 `BroadcastChannel('muvee-auth')` 通知你的页面。SDK 收到后调用 `getUser()`，`wait()` 以用户信息 resolve。收不到消息时，窗口重新获得焦点会兜底再检查一次。

会话就是弹窗最后一次重定向给项目种下的 host-only cookie；登录流程不会把 token 或身份返回给你的代码。`POST /_oauth/login-token` 与 `/poll` 仍然保留，但 `poll` 成功时只返回 `{"status":"success"}`，**永远不下发身份或凭证**。会话按项目隔离：一个项目的会话拿到别的项目无效。

OAuth 的 client_id、scope 和 redirect URL 都保存在 muvee 服务端。SPA 代码完全碰不到这些东西，所以哪怕轮换凭据也不会让已发布的客户端失效。

## 按项目维度配置启用的 provider

每个项目有一个 `enabled_providers` 字段（逗号分隔，比如 `google,feishu`）。留空 = 继承 muvee 平台上所有全局已启用的 provider，这也是已有项目升级后的默认值。在项目设置页的 **Sign-in providers** 区块里勾选具体的 provider。

`MuveeAuth.listProviders()` 返回的是：项目的白名单 ∩ muvee 实际加载的 provider，按固定顺序（`google`、`feishu`、`wecom`、`dingtalk`）。

## 安装

```bash
npm install @muvee/auth
```

## React + Vite

```tsx
import { useEffect, useState } from 'react';
import { MuveeAuth, type MuveeUser, type ProviderInfo } from '@muvee/auth';

MuveeAuth.configure({}); // baseUrl 默认取 window.location.origin

export function SignIn() {
  const [providers, setProviders] = useState<ProviderInfo[]>([]);
  const [user, setUser] = useState<MuveeUser | null>(null);

  useEffect(() => {
    MuveeAuth.listProviders().then(setProviders);
    MuveeAuth.getUser().then(setUser);
    return MuveeAuth.onAuthChange(setUser);
  }, []);

  if (user) {
    return (
      <div>
        <p>你好 {user.name}</p>
        <button onClick={() => MuveeAuth.signOut()}>退出登录</button>
      </div>
    );
  }

  // 在点击处理器里调用：signIn 必须同步调用（前面不能有 await），弹窗才不会被拦截。
  const start = async (provider: string) => {
    const handle = MuveeAuth.signIn(provider);
    if (!handle.popup) {
      // 弹窗被拦截：把 handle.url 渲染成链接，让用户自己点开。
    }
    const u = await handle.wait();
    setUser(u);
  };

  return providers.map(p => (
    <button key={p.name} onClick={() => start(p.name)}>
      使用 {p.display_name} 登录
    </button>
  ));
}
```

## 弹窗被拦截

浏览器拦截弹窗时 `handle.popup` 为 `null`。把 `handle.url` 显示成链接，让用户通过点击打开。也可以在点击处理器里自己先开一个窗口再传给 SDK，由 SDK 导航它：

```ts
const popup = window.open('', 'muvee-sign-in');
const handle = MuveeAuth.signIn('google', { popup, timeoutMs: 5 * 60_000 });
```

`wait()` 在 `timeoutMs`（默认 10 分钟）后放弃并以 `code: 'expired'` reject；`handle.cancel()` 会关闭弹窗并以 `code: 'cancelled'` reject。

## 从 0.1.x 升级

`signIn()` 不再是 async，宿主也不再自己打开 `oauthUrl`：

```diff
-const handle = await MuveeAuth.signIn(provider);
-window.open(handle.oauthUrl, '_blank');
-const user = await handle.wait();
+const handle = MuveeAuth.signIn(provider); // 同步，自动打开弹窗
+if (!handle.popup) { /* 被拦截：让用户点开 handle.url */ }
+const user = await handle.wait();
```

- `handle.oauthUrl` 是 `handle.url` 的弃用别名；`signIn` 已经打开过它，不要再打开一次。
- `configure()` 的 `minPollIntervalSec` 已弃用，不再有任何作用。
- 0.1.x 的 `signIn` 在当前 muvee 上不会抛错，但也不会让用户登录：`wait()` 以 `undefined` resolve 且不建立会话，因为 `poll` 现在只回 `{"status":"success"}`、不带 `user`。请升级到 `^0.2.0`。
- `baseUrl` 必须与页面同源。0.2.0 下让本地开发页（如 `localhost`）去连远端项目不可行：弹窗完成通知的目标 origin 是页面自身，SDK 只接受来自 `baseUrl` origin 的消息，`BroadcastChannel` 同源，跨站的 `getUser()` 也不会带会话 cookie。请到项目自己域名下的主机上调试。

## Tauri / React Native

自 0.2.0 起不再支持：不要在 Tauri、React Native、Electron 等非浏览器宿主中调用 `signIn()`。会话是跑你页面的那个浏览器里的 cookie。只有完全没有全局 `window` 的环境才会抛出 `AuthError('unsupported')`（若尚未配置 `baseUrl`，会先抛 `not_configured`）；Tauri / Electron 的 webview 里有 `window`，`signIn` 会继续走到 `window.open`，React Native 里通常会在那里抛 `TypeError`。

## 跨 tab 同步

`onAuthChange` 在以下三种场景下会在 **同一项目子域** 的每个 tab 里被触发：

- 当前 tab 完成了 `signIn()`
- 当前 tab 调用了 `signOut()`
- 其他 tab 做了上面任意一件事

底层用 `BroadcastChannel` 实现，对于没有 `BroadcastChannel` 的浏览器会自动 fallback 到 `localStorage` 事件。

## 登出

`MuveeAuth.signOut()` 登出当前项目，同时结束平台在该基域下的中心登录会话，因此同一基域下的其他 muvee 项目会在下一次请求时重新要求登录（用户会看到「已登出」页）。不会登出 Google 等 provider 本身的会话。页面保持控制权：不会跟随登出重定向。

## 错误

`MuveeAuth.signIn(...).wait()` 会以 `AuthError` reject：

| `error.code`         | 含义                                                           |
|----------------------|----------------------------------------------------------------|
| `expired`            | 超过 `timeoutMs`（默认 10 分钟）用户仍未完成登录。             |
| `cancelled`          | 你的代码调了 `handle.cancel()`。                               |

`signIn()` 同步抛出以下错误（不经 `wait()`）；其余方法是 async，以 rejected promise 返回：

| `error.code`         | 含义                                                           |
|----------------------|----------------------------------------------------------------|
| `not_configured`     | 没有 `baseUrl` 且没有 `window` 可推导；请先调用 `configure({ baseUrl })`。`signIn` 同步抛出；`getUser`、`listProviders`、`signOut` 以它 reject。 |
| `unsupported`        | 在没有全局 `window` 的环境里调用了 `signIn()`。                |

`getUser()` 与 `listProviders()` 在底层 HTTP 失败时以 `http_<status>` reject（`getUser()` 会把 `401` 转成 `null`）。`signOut()` 会吞掉网络错误（无法确定 `baseUrl` 时仍会以 `not_configured` reject）。

## 设计上**故意没有**的东西

- **不返回 bearer token**。这套 SDK 面向项目下游用户，他们不需要调 muvee 平台 API。
- **登录流程的响应里没有身份或凭证**。会话是 host-only cookie；你的代码只能通过 `getUser()` / `/_oauth/userinfo` 得知是谁登录了。
- **不提供持久化 API**。session 走 muvee 的 HttpOnly cookie，调 `getUser()` 恢复状态。
