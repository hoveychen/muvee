# SPA Auth SDK (`@muvee/auth`)

Sign-in SDK for **project downstream users** — the people who use the apps you
deploy on muvee, not the people who manage muvee itself. Same `@muvee/auth`
package works in web SPAs, Tauri, Electron, and React Native: a polled
`login_token` flow hides every cross-tab / cross-origin / cross-process detail
behind two API calls.

## How it works

1. **`MuveeAuth.signIn(provider)`** asks the server to mint a `login_token`
   and returns an `oauth_url` plus a `wait()` promise.
2. **Your code opens `oauth_url`** however it likes — new tab, popup, Tauri
   shell, React Native `Linking`. The user signs in with the provider.
3. **`wait()` resolves** when the server-side OAuth callback completes. There
   is no `postMessage` handshake — the SDK is polling `login_token` in the
   background and the callback page just auto-closes.

The OAuth client_id, scopes, and redirect URLs live on muvee. SPA code never
touches them, so rotating credentials never breaks already-shipped clients.

## Per-project provider whitelist

Each project carries an `enabled_providers` field (comma-separated, e.g.
`google,feishu`). Empty means "inherit every globally-configured provider"
which is also the migration default for existing projects. Pick the providers
on the project's settings page under **Sign-in providers**.

`MuveeAuth.listProviders()` returns the intersection of the project's
whitelist and the providers actually loaded by muvee, in canonical order
(`google`, `feishu`, `wecom`, `dingtalk`).

## Install

```bash
npm install @muvee/auth
```

## React + Vite

```tsx
import { useEffect, useState } from 'react';
import { MuveeAuth, type MuveeUser, type ProviderInfo } from '@muvee/auth';

MuveeAuth.configure({}); // baseUrl defaults to window.location.origin

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
        <p>Hi {user.name}</p>
        <button onClick={() => MuveeAuth.signOut()}>Sign out</button>
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
      Continue with {p.display_name}
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
await open(handle.oauthUrl);    // default browser
const user = await handle.wait();
```

The desktop app can keep polling indefinitely (the user might fight a captcha
for a while); the server-side `login_token` has a 10 minute TTL after which
`wait()` rejects with `code: 'expired'`.

## React Native

```ts
import { Linking } from 'react-native';
import { MuveeAuth } from '@muvee/auth';

MuveeAuth.configure({ baseUrl: 'https://my-app.example.com' });

const handle = await MuveeAuth.signIn('google');
Linking.openURL(handle.oauthUrl);
const user = await handle.wait();
```

## Cross-tab sync

`onAuthChange` fires on every tab of the same project subdomain when:

- this tab finishes a `signIn()`
- this tab calls `signOut()`
- another tab does either of the above

It works via `BroadcastChannel` with a `localStorage` event fallback for
browsers without `BroadcastChannel`.

## Errors

`MuveeAuth.signIn(...).wait()` rejects with `AuthError`:

| `error.code`         | Meaning                                                        |
|----------------------|----------------------------------------------------------------|
| `expired`            | Server TTL elapsed before the user finished the OAuth screen.  |
| `cancelled`          | Your code called `handle.cancel()`.                            |
| `provider_error`     | The OAuth provider returned an error; see `error.message`.     |
| `http_<status>`      | Underlying HTTP failure (network, auth, ...).                  |

## What does NOT exist by design

- **No bearer token return.** The SDK targets project downstream users, who
  don't need to call muvee platform APIs.
- **No automatic popup management.** The host environment decides how
  `oauth_url` opens. This makes the SDK identical in shape between web,
  desktop, and mobile.
- **No persistence APIs.** Session lives in muvee's HttpOnly cookie (web) or
  is re-issued on each `signIn()` (desktop / RN). `getUser()` rehydrates.
