---
id: spa-auth-sdk
title: SPA Auth SDK
sidebar_position: 7
---

# SPA Auth SDK (`@muvee/auth`)

Sign-in SDK for **project downstream users** — the people who use the apps you
deploy on muvee, not the people who manage muvee itself. Same `@muvee/auth`
package runs in the browser: `signIn()` opens muvee's standard sign-in in a
popup and `wait()` resolves once the popup has set the project's session
cookie. The SDK never handles a credential. `baseUrl` must be the same origin
as the page that calls the SDK (the default `window.location.origin` is right):
the pop-up notification, the cross-tab sync and the cookie-based `getUser()` all
rely on it, so you cannot point a page on another site at a remote project.

## How it works

1. **`MuveeAuth.signIn(provider)`** synchronously opens a popup at
   `{baseUrl}/_oauth/login?provider=<p>&popup=1` and returns a `SignInHandle`.
   Call it directly in a click handler, before any `await`, so the browser
   does not block the popup.
2. **The user signs in in the popup.** Because `signIn` always passes
   `provider=`, the popup goes through the platform's central sign-in host
   straight into the chosen provider: it does not show the project's login
   page and does not try a silent platform sign-in first. The user passes
   through the provider every time (if the provider itself still has a
   session, they may not need to type anything).
3. **The popup lands on `/_oauth/popup-done`**, which notifies your page via
   `postMessage` (target origin pinned to your project's origin) and
   `BroadcastChannel('muvee-auth')`. The SDK then calls `getUser()` and
   `wait()` resolves with the user. If no message arrives, the SDK re-checks
   when the window regains focus.

The session is the project's host-only cookie set by the popup's final
redirect; no token or identity is returned to your code by the sign-in flow.
`POST /_oauth/login-token` and `/poll` still exist, but `poll` now only
reports `{"status":"success"}` and never returns an identity or credential.
Sessions are isolated per project: a session from one project is not valid on
another.

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

  // Runs in the click handler: signIn must be called synchronously (before
  // any await) so the popup is not blocked.
  const start = async (provider: string) => {
    const handle = MuveeAuth.signIn(provider);
    if (!handle.popup) {
      // Popup blocked: show handle.url as a link for the user to open.
    }
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

## Popup blocked

If the browser blocks the popup, `handle.popup` is `null`. Show `handle.url`
as a link and let the user open it from a click. Alternatively open a window
yourself in the click handler and pass it in; the SDK navigates it:

```ts
const popup = window.open('', 'muvee-sign-in');
const handle = MuveeAuth.signIn('google', { popup, timeoutMs: 5 * 60_000 });
```

`wait()` gives up after `timeoutMs` (default 10 minutes) and rejects with
`code: 'expired'`. `handle.cancel()` closes the popup and rejects with
`code: 'cancelled'`.

## Upgrading from 0.1.x

`signIn()` is no longer async and the host no longer opens `oauthUrl`:

```diff
-const handle = await MuveeAuth.signIn(provider);
-window.open(handle.oauthUrl, '_blank');
-const user = await handle.wait();
+const handle = MuveeAuth.signIn(provider); // synchronous, opens the popup
+if (!handle.popup) { /* blocked: let the user open handle.url */ }
+const user = await handle.wait();
```

- `handle.oauthUrl` is a deprecated alias of `handle.url`; `signIn` already
  opened it, so do not open it again.
- `minPollIntervalSec` in `configure()` is a deprecated no-op.
- 0.1.x `signIn` does not throw against current muvee, but it does not sign
  the user in either: `wait()` resolves with `undefined` and no session is
  established, because `poll` now returns `{"status":"success"}` without a
  `user`. Upgrade to `^0.2.0`.
- `baseUrl` must be the same origin as your page. Pointing a local dev page
  (for example `localhost`) at a remote project does not work in 0.2.0: the
  pop-up notification targets the page's own origin, the SDK only accepts
  messages from the `baseUrl` origin, `BroadcastChannel` is same-origin, and a
  cross-site `getUser()` carries no session cookie. Debug on a host under the
  project's own domain.

## Tauri / React Native

Not supported since 0.2.0: do not call `signIn()` from Tauri, React Native,
Electron or other non-browser hosts. The session is a browser cookie set in
the browser that runs your page. Only an environment with no global `window`
at all throws `AuthError('unsupported')` (if `baseUrl` was never configured,
`not_configured` is thrown first); in a Tauri/Electron webview `window`
exists, so `signIn` goes on to `window.open`, and in React Native it typically
fails with a `TypeError` there.

## Cross-tab sync

`onAuthChange` fires on every tab of the same project subdomain when:

- this tab finishes a `signIn()`
- this tab calls `signOut()`
- another tab does either of the above

It works via `BroadcastChannel` with a `localStorage` event fallback for
browsers without `BroadcastChannel`.

## Sign out

`MuveeAuth.signOut()` signs the user out of this project and also ends the
platform's central sign-in session for the base domain, so other muvee
projects under the same domain ask again on their next request (the user sees
their "signed out" page). The identity provider's own session, e.g. Google, is
untouched. The page stays in control: the logout redirect is not followed.

## Errors

`MuveeAuth.signIn(...).wait()` rejects with `AuthError`:

| `error.code`         | Meaning                                                        |
|----------------------|----------------------------------------------------------------|
| `expired`            | `timeoutMs` (default 10 minutes) elapsed before sign-in finished. |
| `cancelled`          | Your code called `handle.cancel()`.                            |

`signIn()` throws these synchronously (not through `wait()`); the other
methods are async and return them as a rejected promise:

| `error.code`         | Meaning                                                        |
|----------------------|----------------------------------------------------------------|
| `not_configured`     | No `baseUrl` and no `window` to derive it from; call `configure({ baseUrl })` first. Thrown synchronously by `signIn`; `getUser`, `listProviders` and `signOut` reject with it. |
| `unsupported`        | `signIn()` was called where there is no global `window`.       |

`getUser()` and `listProviders()` reject with `http_<status>` on an underlying
HTTP failure (`getUser()` turns `401` into `null`). `signOut()` swallows
network errors (it still rejects with `not_configured` when no `baseUrl` can be
resolved).

## What does NOT exist by design

- **No bearer token return.** The SDK targets project downstream users, who
  don't need to call muvee platform APIs.
- **No identity or credential in the sign-in flow's responses.** The session is
  a host-only cookie; your code learns who signed in only by calling
  `getUser()` / `/_oauth/userinfo`.
- **No persistence APIs.** Session lives in muvee's HttpOnly cookie.
  `getUser()` rehydrates.
