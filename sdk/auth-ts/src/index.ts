// muvee project sign-in SDK.
//
// Flow: SDK posts /_oauth/login-token → server returns { login_token, oauth_url }.
// The host environment opens oauth_url (window.open, Tauri shell, RN Linking).
// The SDK polls /_oauth/login-token/poll until the OAuth callback flips the
// server-side entry to "success", at which point poll returns the user. The
// completion window auto-closes; no postMessage, no cross-origin handshake.
//
// SDK consumers never see the OAuth client_id or scopes — muvee assembles the
// authorization URL on the server. The SDK is intentionally agnostic to how
// the URL is opened so the same package works for web SPAs, Tauri desktop
// apps, Electron, and React Native.

export interface MuveeAuthConfig {
  /** Base URL of the project subdomain, e.g. "https://my-app.example.com". Defaults to window.location.origin. */
  baseUrl?: string;
  /** Floor on the poll loop in seconds. Server-supplied poll_interval still wins when larger. */
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

export interface SignInHandle {
  /**
   * OAuth authorization URL the host should open. Web: window.open(url, '_blank').
   * Tauri: import('@tauri-apps/api/shell').open(url). RN: Linking.openURL(url).
   */
  oauthUrl: string;
  /** Resolves with the authenticated user once the OAuth round-trip completes. */
  wait(): Promise<MuveeUser>;
  /** Stop polling. The login_token expires server-side on its own TTL. */
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
  minPollSec: number;
  listeners: Set<AuthChangeListener>;
  lastUser: MuveeUser | null;
  channel: BroadcastChannel | null;
}

const BROADCAST_CHANNEL = 'muvee-auth';
const STORAGE_KEY = 'muvee-auth:lastChange';

const state: InternalState = {
  baseUrl: '',
  minPollSec: 2,
  listeners: new Set(),
  lastUser: null,
  channel: null,
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

function attachBroadcast(): void {
  if (state.channel || typeof BroadcastChannel === 'undefined') return;
  state.channel = new BroadcastChannel(BROADCAST_CHANNEL);
  state.channel.onmessage = (ev) => {
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

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export const MuveeAuth = {
  configure(config: MuveeAuthConfig = {}): void {
    if (config.baseUrl) {
      state.baseUrl = config.baseUrl.replace(/\/+$/, '');
    } else if (!state.baseUrl && typeof window !== 'undefined') {
      state.baseUrl = window.location.origin;
    }
    if (config.minPollIntervalSec && config.minPollIntervalSec > 0) {
      state.minPollSec = config.minPollIntervalSec;
    }
    attachBroadcast();
  },

  async listProviders(): Promise<ProviderInfo[]> {
    ensureConfigured();
    return jsonFetch<ProviderInfo[]>(`${state.baseUrl}/_oauth/providers`, {
      method: 'GET',
    });
  },

  async signIn(provider: string): Promise<SignInHandle> {
    ensureConfigured();
    attachBroadcast();
    const created = await jsonFetch<{
      login_token: string;
      oauth_url: string;
      expires_in: number;
      poll_interval: number;
    }>(`${state.baseUrl}/_oauth/login-token`, {
      method: 'POST',
      body: JSON.stringify({ provider }),
    });

    const pollIntervalSec = Math.max(state.minPollSec, created.poll_interval || state.minPollSec);
    const deadline = Date.now() + created.expires_in * 1000;
    let cancelled = false;

    const wait = async (): Promise<MuveeUser> => {
      while (!cancelled) {
        if (Date.now() > deadline) {
          throw new AuthError('expired', 'sign-in timed out');
        }
        await delay(pollIntervalSec * 1000);
        if (cancelled) {
          throw new AuthError('cancelled', 'sign-in cancelled');
        }
        const status = await jsonFetch<
          | { status: 'pending' }
          | { status: 'success'; user: MuveeUser }
          | { status: 'error'; error: string }
          | { status: 'expired' }
        >(`${state.baseUrl}/_oauth/login-token/poll`, {
          method: 'POST',
          body: JSON.stringify({ login_token: created.login_token }),
        });
        if (status.status === 'pending') continue;
        if (status.status === 'success') {
          emit(status.user, true);
          return status.user;
        }
        if (status.status === 'expired') {
          throw new AuthError('expired', 'sign-in expired before completion');
        }
        throw new AuthError('provider_error', status.error || 'provider error');
      }
      throw new AuthError('cancelled', 'sign-in cancelled');
    };

    return {
      oauthUrl: created.oauth_url,
      wait,
      cancel() {
        cancelled = true;
      },
    };
  },

  async signOut(opts: { redirect?: string } = {}): Promise<void> {
    ensureConfigured();
    const url = new URL(`${state.baseUrl}/_oauth/logout`);
    if (opts.redirect) url.searchParams.set('redirect', opts.redirect);
    // /_oauth/logout responds with a 302 — we don't follow it so the SPA
    // stays in control. Just hitting the endpoint clears the cookie.
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
