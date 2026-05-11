---
id: spa-auth-sdk
title: SPA 鉴权 SDK
sidebar_position: 5
---

# SPA 鉴权 SDK（`@muvee/auth`）

面向 **项目下游用户** 的登录 SDK —— 也就是使用你在 muvee 上部署的应用的人，而**不是**管理 muvee 平台本身的人。同一份 `@muvee/auth` 包既能在 Web SPA 里跑，也能在 Tauri、Electron、React Native 里跑：用 `login_token` 轮询的方式把跨 tab、跨域、跨进程的细节都藏在两个 API 调用背后。

## 工作原理

1. **`MuveeAuth.signIn(provider)`** 让服务器签发一个 `login_token`，同时返回 `oauth_url` 和一个 `wait()` Promise。
2. **你的代码自己决定怎么打开 `oauth_url`** —— 新 tab、弹窗、Tauri shell、React Native `Linking` 都可以。用户在该 provider 完成登录。
3. **`wait()` 在服务端 OAuth 回调完成后 resolve**。整个过程没有 `postMessage` 握手 —— SDK 在后台轮询 `login_token`，回调页面看到结果后会自动关闭。

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

  const start = async (provider: string) => {
    const handle = await MuveeAuth.signIn(provider);
    window.open(handle.oauthUrl, '_blank');
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

## Tauri 2

```ts
import { open } from '@tauri-apps/api/shell';
import { MuveeAuth } from '@muvee/auth';

MuveeAuth.configure({ baseUrl: 'https://my-app.example.com' });

const handle = await MuveeAuth.signIn('google');
await open(handle.oauthUrl);    // 用系统默认浏览器打开
const user = await handle.wait();
```

桌面应用可以一直轮询下去（用户可能要花点时间过验证码），服务器侧的 `login_token` 默认 TTL 是 10 分钟，超时后 `wait()` 会以 `code: 'expired'` reject。

## React Native

```ts
import { Linking } from 'react-native';
import { MuveeAuth } from '@muvee/auth';

MuveeAuth.configure({ baseUrl: 'https://my-app.example.com' });

const handle = await MuveeAuth.signIn('google');
Linking.openURL(handle.oauthUrl);
const user = await handle.wait();
```

## 跨 tab 同步

`onAuthChange` 在以下三种场景下会在 **同一项目子域** 的每个 tab 里被触发：

- 当前 tab 完成了 `signIn()`
- 当前 tab 调用了 `signOut()`
- 其他 tab 做了上面任意一件事

底层用 `BroadcastChannel` 实现，对于没有 `BroadcastChannel` 的浏览器会自动 fallback 到 `localStorage` 事件。

## 错误

`MuveeAuth.signIn(...).wait()` 会以 `AuthError` reject：

| `error.code`         | 含义                                                           |
|----------------------|----------------------------------------------------------------|
| `expired`            | 服务端 TTL 到了，用户还没走完 OAuth 流程。                     |
| `cancelled`          | 你的代码调了 `handle.cancel()`。                               |
| `provider_error`     | OAuth provider 返了错；详情看 `error.message`。                |
| `http_<status>`      | 底层 HTTP 失败（网络、鉴权等）。                               |

## 设计上**故意没有**的东西

- **不返回 bearer token**。这套 SDK 面向项目下游用户，他们不需要调 muvee 平台 API。
- **不自动管理弹窗**。`oauth_url` 怎么打开由宿主环境自己决定 —— 这让 SDK 在 Web、桌面、移动端的形状完全一致。
- **不提供持久化 API**。Web 端的 session 走 muvee 的 HttpOnly cookie；桌面端 / RN 每次 `signIn()` 都重新发一次，再调 `getUser()` 恢复状态。
