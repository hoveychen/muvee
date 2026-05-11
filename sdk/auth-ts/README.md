# @muvee/auth

Sign-in SDK for muvee projects. Polled `login_token` flow over OAuth — no
postMessage handshake, no popup origin gymnastics. The same package works in
web SPAs, Tauri, Electron, and React Native.

## Install

```bash
npm install @muvee/auth
```

## Quick start (Web)

```ts
import { MuveeAuth } from '@muvee/auth';

MuveeAuth.configure({}); // baseUrl defaults to window.location.origin

const providers = await MuveeAuth.listProviders();

async function signIn(provider: string) {
  const handle = await MuveeAuth.signIn(provider);
  window.open(handle.oauthUrl, '_blank'); // user signs in, closes tab
  const user = await handle.wait();
  console.log('signed in as', user.email);
}

MuveeAuth.onAuthChange((user) => {
  console.log('auth state →', user);
});
```

## Tauri

```ts
import { open } from '@tauri-apps/api/shell';
import { MuveeAuth } from '@muvee/auth';

const handle = await MuveeAuth.signIn('google');
await open(handle.oauthUrl);
const user = await handle.wait();
```

## React Native

```ts
import { Linking } from 'react-native';
import { MuveeAuth } from '@muvee/auth';

const handle = await MuveeAuth.signIn('google');
Linking.openURL(handle.oauthUrl);
const user = await handle.wait();
```

## API

- `MuveeAuth.configure(config)`
- `MuveeAuth.listProviders(): Promise<ProviderInfo[]>`
- `MuveeAuth.signIn(provider): Promise<SignInHandle>` — host opens `handle.oauthUrl`, then `await handle.wait()`
- `MuveeAuth.signOut({ redirect? }): Promise<void>`
- `MuveeAuth.getUser(): Promise<MuveeUser | null>`
- `MuveeAuth.onAuthChange(cb): () => void` — cross-tab sync via BroadcastChannel + storage events

See [`docs/SPA_AUTH_SDK_DESIGN.md`](../../docs/SPA_AUTH_SDK_DESIGN.md) for the full design.
