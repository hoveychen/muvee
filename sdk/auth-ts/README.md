# @muvee/auth

Sign-in SDK for muvee projects. Since 0.2.0 `signIn()` opens muvee's standard
browser sign-in in a popup and resolves once the popup has set the project's
session cookie — the SDK never handles a credential. Browser only: the session
is a cookie set in the browser that runs your page, so `baseUrl` (default
`window.location.origin`) must be the same origin as your page.

## Install

```bash
npm install @muvee/auth
```

## Quick start (Web)

```ts
import { MuveeAuth } from '@muvee/auth';

MuveeAuth.configure({}); // baseUrl defaults to window.location.origin

const providers = await MuveeAuth.listProviders();

// Call from a click handler. signIn is synchronous and opens the popup itself,
// so do not `await` anything before it or the browser may block the popup.
function signIn(provider: string) {
  const handle = MuveeAuth.signIn(provider);
  if (!handle.popup) {
    // Popup blocked: show handle.url as a link and let the user open it.
  }
  return handle.wait().then((user) => {
    console.log('signed in as', user.email);
  });
}

MuveeAuth.onAuthChange((user) => {
  console.log('auth state →', user);
});
```

## Upgrading from 0.1.x

`signIn()` is no longer async and no longer expects the host to open
`oauthUrl`. 0.1.x polled `/_oauth/login-token` for the identity; muvee no
longer reveals the identity there, so a 0.1.x `signIn` against current muvee
resolves `wait()` with `undefined` and establishes no session (poll returns
`{"status":"success"}` without a `user`).

```diff
-const handle = await MuveeAuth.signIn(provider);
-window.open(handle.oauthUrl, '_blank');
-const user = await handle.wait();
+const handle = MuveeAuth.signIn(provider); // synchronous, opens the popup
+if (!handle.popup) { /* blocked: let the user open handle.url */ }
+const user = await handle.wait();
```

- Call `signIn` synchronously inside the click handler (before any `await`).
- `handle.oauthUrl` is a deprecated alias of `handle.url`; `signIn` already
  opened it, so do not open it again.
- `minPollIntervalSec` in `configure()` is a deprecated no-op.
- Since 0.2.0 non-browser hosts (Tauri, React Native, Electron, ...) are not
  supported: do not call `signIn()` there. Only an environment with no global
  `window` at all throws `AuthError('unsupported')` (if `baseUrl` is not
  configured, `not_configured` is thrown first).
- `baseUrl` must be the same origin as your page: the popup notification,
  cross-tab sync and cookie-based `getUser()` all rely on it.

## API

- `MuveeAuth.configure(config)` — `{ baseUrl? }` (`minPollIntervalSec` is deprecated, no-op)
- `MuveeAuth.listProviders(): Promise<ProviderInfo[]>`
- `MuveeAuth.signIn(provider, opts?): SignInHandle` — synchronous; opens the popup
  - `opts.popup?: Window` — a window you already opened in the click handler
    (the SDK navigates it)
  - `opts.timeoutMs?: number` — how long `wait()` waits (default 10 minutes)
  - `SignInHandle`: `url` (sign-in page), `popup` (`Window | null`, `null` if
    blocked), `wait(): Promise<MuveeUser>`, `cancel()`, and the deprecated
    `oauthUrl` (= `url`)
  - `wait()` rejects with `AuthError`: `expired` (timeout), `cancelled`
    (`cancel()` closes the popup)
  - Throws `AuthError('unsupported')` when there is no global `window`
    (`not_configured` first if `baseUrl` is unset)
  - The popup goes through the central sign-in host straight into the chosen
    provider (no project login page)
- `MuveeAuth.signOut({ redirect? }): Promise<void>` — signs out of this project
  and ends the platform's central sign-in session for the base domain; other
  muvee projects under the same domain ask again on their next request
- `MuveeAuth.getUser(): Promise<MuveeUser | null>` — `401` becomes `null`; other HTTP failures reject with `http_<status>` (`signOut()` swallows errors and does not reject)
- `MuveeAuth.onAuthChange(cb): () => void` — cross-tab sync via BroadcastChannel + storage events

See [`docs/SPA_AUTH_SDK_DESIGN.md`](../../docs/SPA_AUTH_SDK_DESIGN.md) for the full design.
