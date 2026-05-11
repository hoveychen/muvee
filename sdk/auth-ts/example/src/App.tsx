import { useEffect, useState } from 'react';
import { MuveeAuth, type MuveeUser, type ProviderInfo } from '@muvee/auth';

MuveeAuth.configure({});

export function App() {
  const [providers, setProviders] = useState<ProviderInfo[]>([]);
  const [user, setUser] = useState<MuveeUser | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    MuveeAuth.listProviders().then(setProviders).catch((e: Error) => setError(e.message));
    MuveeAuth.getUser().then(setUser).catch(() => {});
    return MuveeAuth.onAuthChange(setUser);
  }, []);

  const start = async (provider: string) => {
    setError(null);
    setBusy(true);
    try {
      const handle = await MuveeAuth.signIn(provider);
      const popup = window.open(handle.oauthUrl, '_blank');
      if (!popup) {
        handle.cancel();
        setError('Popup blocked — please allow popups for this site and retry.');
        return;
      }
      const u = await handle.wait();
      setUser(u);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const signOut = async () => {
    setBusy(true);
    try {
      await MuveeAuth.signOut();
    } finally {
      setBusy(false);
    }
  };

  return (
    <main style={styles.page}>
      <section style={styles.card}>
        <h1 style={styles.h1}>@muvee/auth demo</h1>
        <p style={styles.muted}>
          Polled <code>login_token</code> flow — open the provider in a new tab,
          finish sign-in, close the tab. This page poll-detects success.
        </p>

        {user ? (
          <div style={styles.userBlock}>
            {user.avatar_url && (
              <img src={user.avatar_url} alt="" style={styles.avatar} />
            )}
            <div>
              <div style={styles.userName}>{user.name || user.email}</div>
              <div style={styles.muted}>{user.email}</div>
              <div style={styles.muted}>via {user.provider}</div>
            </div>
            <button onClick={signOut} disabled={busy} style={styles.signOutBtn}>
              Sign out
            </button>
          </div>
        ) : (
          <div style={styles.providers}>
            {providers.length === 0 && !error && <p style={styles.muted}>Loading providers…</p>}
            {providers.map(p => (
              <button
                key={p.name}
                onClick={() => start(p.name)}
                disabled={busy}
                style={styles.btn}
              >
                Continue with {p.display_name}
              </button>
            ))}
          </div>
        )}

        {error && <p style={styles.error}>{error}</p>}
      </section>
    </main>
  );
}

const styles: Record<string, React.CSSProperties> = {
  page: {
    minHeight: '100vh',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    background: '#f5f5f5',
    fontFamily: 'system-ui, sans-serif',
    margin: 0,
  },
  card: {
    background: '#fff',
    borderRadius: 12,
    padding: '2.5rem 3rem',
    boxShadow: '0 4px 24px rgba(0,0,0,.08)',
    minWidth: 360,
    maxWidth: 480,
  },
  h1: { margin: 0, fontSize: '1.3rem', color: '#111' },
  muted: { color: '#666', fontSize: '0.875rem', margin: '0.5rem 0' },
  providers: { display: 'flex', flexDirection: 'column', gap: '0.5rem', marginTop: '1.5rem' },
  btn: {
    padding: '0.75rem 1.25rem',
    borderRadius: 8,
    background: '#4f46e5',
    color: '#fff',
    border: 'none',
    fontSize: '0.95rem',
    cursor: 'pointer',
  },
  signOutBtn: {
    padding: '0.4rem 0.9rem',
    borderRadius: 6,
    background: '#f3f4f6',
    color: '#111',
    border: '1px solid #e5e7eb',
    fontSize: '0.85rem',
    cursor: 'pointer',
    marginLeft: 'auto',
  },
  userBlock: {
    display: 'flex',
    alignItems: 'center',
    gap: '0.75rem',
    marginTop: '1.5rem',
    padding: '0.75rem',
    border: '1px solid #e5e7eb',
    borderRadius: 8,
  },
  avatar: { width: 48, height: 48, borderRadius: 24 },
  userName: { fontWeight: 600, color: '#111' },
  error: { color: '#dc2626', fontSize: '0.875rem', marginTop: '1rem' },
};
