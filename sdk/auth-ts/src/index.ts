// muvee project sign-in SDK.
//
// Flow (0.2.0): signIn(provider) opens `{baseUrl}/_oauth/login?provider=p&popup=1`
// in a popup. The popup runs muvee's standard browser sign-in — the project's
// own login page, the platform's central sign-in host, the provider — and
// lands on `{baseUrl}/_oauth/popup-done`, which tells this page through
// postMessage (target origin pinned to the project origin) and
// BroadcastChannel('muvee-auth'). The SDK then reads the signed-in user from
// /_oauth/userinfo. If neither message arrives (an identity provider's
// Cross-Origin-Opener-Policy can sever window.opener), the SDK re-checks
// /_oauth/userinfo whenever this window regains focus.
//
// The session itself is the project's host-only cookie set by the popup's
// final redirect; the SDK never handles a credential. 0.1.x polled
// /_oauth/login-token for the identity — muvee no longer reveals the identity
// there (anyone holding the oauth_url could otherwise read the victim's
// identity), so 0.1.x signIn no longer completes.
//
// SDK consumers never see the OAuth client_id or scopes — muvee assembles the
// authorization URL on the server.

export interface MuveeAuthConfig {
  /** Base URL of the project subdomain, e.g. "https://my-app.example.com". Defaults to window.location.origin. */
  baseUrl?: string;
  /** @deprecated No-op since 0.2.0 (sign-in no longer polls). */
  minPollIntervalSec?: number;
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

export interface SignInOptions {
  /**
   * A window you already opened (e.g. `window.open('', 'muvee-sign-in')` in
   * your click handler) for the SDK to navigate. By default signIn opens the
   * popup itself — call it synchronously from the click handler, before any
   * `await`, so the browser does not block the popup.
   */
  popup?: Window | null;
  /** How long wait() waits for the sign-in to finish. Defaults to 10 minutes. */
  timeoutMs?: number;
}

export interface SignInHandle {
  /** The sign-in page the popup was pointed at. */
  url: string;
  /** @deprecated Alias of `url`. signIn already opened it — do not open it again. */
  oauthUrl: string;
  /** The popup window, or null when the browser blocked it (open `url` from a click to retry). */
  popup: Window | null;
  /** Resolves with the authenticated user once the popup finishes signing in. */
  wait(): Promise<MuveeUser>;
  /** Stop waiting and close the popup. */
  cancel(): void;
}

type AuthChangeListener = (user: MuveeUser | null) => void;

class AuthError extends Error {
  constructor(public readonly code: string, message: string) {
    super(message);
    this.name = 'MuveeAuthError';
  }
}

interface InternalState {
  baseUrl: string;
  listeners: Set<AuthChangeListener>;
  lastUser: MuveeUser | null;
  channel: BroadcastChannel | null;
  signInSignals: Set<() => void>;
}

const BROADCAST_CHANNEL = 'muvee-auth';
const STORAGE_KEY = 'muvee-auth:lastChange';
// Matches authservice's sign-in state lifetime: a popup left open longer than
// this can no longer complete.
const DEFAULT_SIGN_IN_TIMEOUT_MS = 10 * 60 * 1000;

const state: InternalState = {
  baseUrl: '',
  listeners: new Set(),
  lastUser: null,
  channel: null,
  signInSignals: new Set(),
};

function ensureConfigured(): void {
  if (state.baseUrl) return;
  if (typeof window !== 'undefined' && window.location) {
    state.baseUrl = window.location.origin;
  } else {
    throw new AuthError(
      'not_configured',
      'MuveeAuth.configure({ baseUrl }) must be called before SDK methods in non-browser environments.',
    );
  }
}

function baseOrigin(): string {
  try {
    return new URL(state.baseUrl).origin;
  } catch {
    return '';
  }
}

// isSignInSignal recognises the popup-done page's message. The same
// BroadcastChannel also carries this SDK's cross-tab user sync (a MuveeUser or
// null), which never has a `type` field.
function isSignInSignal(data: unknown): boolean {
  return (
    typeof data === 'object' &&
    data !== null &&
    (data as { type?: unknown }).type === 'muvee-auth' &&
    (data as { status?: unknown }).status === 'signed-in'
  );
}

function notifySignIn(): void {
  for (const signal of Array.from(state.signInSignals)) {
    signal();
  }
}

function attachBroadcast(): void {
  if (state.channel || typeof BroadcastChannel === 'undefined') return;
  state.channel = new BroadcastChannel(BROADCAST_CHANNEL);
  state.channel.onmessage = (ev) => {
    if (isSignInSignal(ev.data)) {
      notifySignIn();
      return;
    }
    const next = (ev.data ?? null) as MuveeUser | null;
    emit(next, false);
  };
  if (typeof window !== 'undefined') {
    window.addEventListener('storage', (ev) => {
      if (ev.key !== STORAGE_KEY) return;
      // The value is a serialised MuveeUser or "null" — parse and re-emit so
      // browsers without BroadcastChannel (Safari < 15.4) still get cross-tab
      // sync.
      try {
        const parsed = ev.newValue ? (JSON.parse(ev.newValue) as MuveeUser) : null;
        emit(parsed, false);
      } catch {
        // ignore malformed payloads
      }
    });
    // The popup-done page posts to its opener with the target origin pinned
    // to the project origin; still check the sender's origin here.
    window.addEventListener('message', (ev) => {
      if (ev.origin !== baseOrigin() || !isSignInSignal(ev.data)) return;
      notifySignIn();
    });
  }
}

function emit(user: MuveeUser | null, broadcast: boolean): void {
  state.lastUser = user;
  for (const cb of state.listeners) {
    try {
      cb(user);
    } catch (err) {
      console.error('MuveeAuth: listener threw', err);
    }
  }
  if (!broadcast) return;
  if (state.channel) {
    state.channel.postMessage(user);
  }
  if (typeof localStorage !== 'undefined') {
    try {
      // Bump the value every emit so listeners on tabs that loaded after this
      // one still fire on any change. JSON.stringify(null) === "null" which is
      // also a valid storage value.
      localStorage.setItem(STORAGE_KEY, JSON.stringify(user));
    } catch {
      // Storage may be disabled (private browsing); ignore.
    }
  }
}

async function jsonFetch<T>(url: string, init: RequestInit): Promise<T> {
  const resp = await fetch(url, {
    ...init,
    credentials: 'include',
    headers: {
      'Content-Type': 'application/json',
      ...(init.headers ?? {}),
    },
  });
  if (!resp.ok) {
    const text = await resp.text().catch(() => '');
    throw new AuthError(`http_${resp.status}`, text || `HTTP ${resp.status}`);
  }
  return (await resp.json()) as T;
}

export const MuveeAuth = {
  configure(config: MuveeAuthConfig = {}): void {
    if (config.baseUrl) {
      state.baseUrl = config.baseUrl.replace(/\/+$/, '');
    } else if (!state.baseUrl && typeof window !== 'undefined') {
      state.baseUrl = window.location.origin;
    }
    attachBroadcast();
  },

  async listProviders(): Promise<ProviderInfo[]> {
    ensureConfigured();
    return jsonFetch<ProviderInfo[]>(`${state.baseUrl}/_oauth/providers`, {
      method: 'GET',
    });
  },

  /**
   * Opens the project's sign-in page for `provider` in a popup and returns at
   * once (synchronously, so it can run inside a click handler without the
   * popup being blocked). `await handle.wait()` for the signed-in user.
   * Browser only: the session is a cookie the popup sets in this browser.
   */
  signIn(provider: string, opts: SignInOptions = {}): SignInHandle {
    ensureConfigured();
    if (typeof window === 'undefined') {
      throw new AuthError('unsupported', 'MuveeAuth.signIn needs a browser window (the session is a browser cookie).');
    }
    attachBroadcast();
    const u = new URL(`${state.baseUrl}/_oauth/login`);
    u.searchParams.set('provider', provider);
    u.searchParams.set('popup', '1');
    const url = u.toString();

    let popup: Window | null = opts.popup ?? null;
    if (popup) {
      popup.location.href = url;
    } else {
      popup = window.open(url, 'muvee-sign-in', 'popup,width=520,height=680');
    }

    let settled = false;
    let cleanup = (): void => {};
    let rejectWait: ((err: AuthError) => void) | null = null;

    const waitPromise = new Promise<MuveeUser>((resolve, reject) => {
      rejectWait = reject;
      let checking = false;
      const check = async (): Promise<void> => {
        if (settled || checking) return;
        checking = true;
        try {
          const user = await MuveeAuth.getUser();
          if (user && !settled) {
            settled = true;
            cleanup();
            resolve(user);
          }
        } catch {
          // Transient failure: the next signal or focus retries.
        } finally {
          checking = false;
        }
      };
      const onFocus = (): void => {
        void check();
      };
      const timer = setTimeout(() => {
        if (settled) return;
        settled = true;
        cleanup();
        reject(new AuthError('expired', 'sign-in timed out'));
      }, opts.timeoutMs ?? DEFAULT_SIGN_IN_TIMEOUT_MS);

      state.signInSignals.add(check);
      window.addEventListener('focus', onFocus);
      cleanup = () => {
        clearTimeout(timer);
        state.signInSignals.delete(check);
        window.removeEventListener('focus', onFocus);
      };
    });
    // Callers that never call wait() must not see an unhandled rejection.
    waitPromise.catch(() => undefined);

    return {
      url,
      oauthUrl: url,
      popup,
      wait: () => waitPromise,
      cancel() {
        if (settled) return;
        settled = true;
        cleanup();
        try {
          popup?.close();
        } catch {
          // ignore
        }
        rejectWait?.(new AuthError('cancelled', 'sign-in cancelled'));
      },
    };
  },

  /**
   * Signs out of this project and ends the platform's central sign-in session
   * for this base domain, so other muvee projects under the same domain ask
   * again on their next request (the user sees their "signed out" page; the
   * identity provider's own session, e.g. Google, is untouched). The page is
   * left in control: the logout redirect is not followed.
   */
  async signOut(opts: { redirect?: string } = {}): Promise<void> {
    ensureConfigured();
    const url = new URL(`${state.baseUrl}/_oauth/logout`);
    if (opts.redirect) url.searchParams.set('redirect', opts.redirect);
    await fetch(url.toString(), {
      method: 'GET',
      credentials: 'include',
      redirect: 'manual',
    }).catch(() => undefined);
    emit(null, true);
  },

  async getUser(): Promise<MuveeUser | null> {
    ensureConfigured();
    try {
      const user = await jsonFetch<MuveeUser>(`${state.baseUrl}/_oauth/userinfo`, {
        method: 'GET',
      });
      if (user.email !== state.lastUser?.email) {
        emit(user, true);
      }
      return user;
    } catch (err) {
      if (err instanceof AuthError && err.code === 'http_401') {
        if (state.lastUser !== null) emit(null, true);
        return null;
      }
      throw err;
    }
  },

  onAuthChange(cb: AuthChangeListener): () => void {
    attachBroadcast();
    state.listeners.add(cb);
    return () => {
      state.listeners.delete(cb);
    };
  },
};

export type { AuthChangeListener };
export { AuthError };
