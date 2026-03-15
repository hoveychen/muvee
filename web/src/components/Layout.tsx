import { ReactNode, useEffect, useState } from 'react'
import { NavLink, Outlet, useNavigate } from 'react-router-dom'
import { LayoutGrid, Database, KeyRound, Server, Users, LogOut } from 'lucide-react'
import { useAuth } from '../lib/auth'
import { api } from '../lib/api'
import type { Node } from '../lib/types'

const MONO = 'var(--font-mono)'

const navItems = [
  { to: '/projects', icon: LayoutGrid, label: 'Projects' },
  { to: '/datasets', icon: Database, label: 'Datasets' },
  { to: '/secrets', icon: KeyRound, label: 'Secrets' },
  { to: '/nodes', icon: Server, label: 'Nodes' },
  { to: '/users', icon: Users, label: 'Users' },
]

export default function Layout({ children }: { children?: ReactNode }) {
  const { user } = useAuth()
  const navigate = useNavigate()

  const handleLogout = async () => {
    await fetch('/auth/logout', { method: 'POST', credentials: 'include' })
    navigate('/login')
  }

  return (
    <div className="flex min-h-screen" style={{ background: 'var(--bg-base)' }}>
      {/* Sidebar */}
      <aside
        className="flex flex-col w-56 flex-shrink-0"
        style={{
          background: 'var(--bg-card)',
          borderRight: '1px solid var(--border)',
          position: 'sticky',
          top: 0,
          height: '100vh',
        }}
      >
        {/* Logo */}
        <div className="px-4 py-5" style={{ borderBottom: '1px solid var(--border)' }}>
          <div style={{ fontFamily: MONO, fontSize: '0.9rem', fontWeight: 600, color: 'var(--fg-primary)' }}>
            muvee
          </div>
          <div style={{ fontFamily: MONO, fontSize: '0.65rem', color: 'var(--fg-muted)', marginTop: '2px' }}>
            private cloud
          </div>
        </div>

        {/* Nav */}
        <nav className="flex-1 py-2">
          {navItems.map(({ to, icon: Icon, label }) => (
            <NavLink
              key={to}
              to={to}
              style={({ isActive }) => ({
                display: 'flex',
                alignItems: 'center',
                gap: '8px',
                padding: '6px 12px',
                margin: '1px 8px',
                borderRadius: '6px',
                fontSize: '0.875rem',
                textDecoration: 'none',
                color: isActive ? 'var(--fg-primary)' : 'var(--fg-muted)',
                background: isActive ? 'var(--bg-hover)' : 'transparent',
                fontWeight: isActive ? 500 : 400,
                transition: 'all 120ms',
              })}
            >
              <Icon size={16} />
              {label}
            </NavLink>
          ))}
        </nav>

        {/* User */}
        {user && (
          <div className="p-3" style={{ borderTop: '1px solid var(--border)' }}>
            <div className="flex items-center gap-2">
              {user.avatar_url ? (
                <img src={user.avatar_url} alt="" className="rounded-full" style={{ width: '24px', height: '24px' }} />
              ) : (
                <div className="rounded-full flex items-center justify-center" style={{ width: '24px', height: '24px', background: 'var(--bg-hover)', fontFamily: MONO, fontSize: '0.65rem', color: 'var(--fg-muted)' }}>
                  {(user.name || user.email || '?').charAt(0).toUpperCase()}
                </div>
              )}
              <div className="flex-1 min-w-0">
                <div style={{ fontSize: '0.8rem', color: 'var(--fg-primary)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontWeight: 500 }}>
                  {user.name || user.email.split('@')[0]}
                </div>
                <div style={{ fontFamily: MONO, fontSize: '0.62rem', color: 'var(--fg-muted)' }}>
                  {user.role}
                </div>
              </div>
              <button
                onClick={handleLogout}
                title="Logout"
                style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--fg-muted)', padding: '4px', borderRadius: '4px' }}
                onMouseEnter={e => { (e.currentTarget as HTMLButtonElement).style.color = 'var(--danger)' }}
                onMouseLeave={e => { (e.currentTarget as HTMLButtonElement).style.color = 'var(--fg-muted)' }}
              >
                <LogOut size={14} />
              </button>
            </div>
          </div>
        )}
      </aside>

      {/* Main content */}
      <main className="flex-1 min-w-0 p-8">
        {children ?? <Outlet />}
      </main>
    </div>
  )
}

export function NodesPage() {
  const [nodes, setNodes] = useState<Node[]>([])
  useEffect(() => { api.nodes.list().then(setNodes).catch(() => {}) }, [])

  const isOnline = (n: Node) => {
    const lastSeen = new Date(n.last_seen_at).getTime()
    return Date.now() - lastSeen < 2 * 60 * 1000
  }

  return (
    <div className="page-enter">
      <div className="mb-8">
        <p style={{ fontFamily: MONO, color: 'var(--fg-muted)', fontSize: '0.7rem', letterSpacing: '0.05em' }}>INFRASTRUCTURE</p>
        <h1 style={{ fontSize: '1.75rem', fontWeight: 700, color: 'var(--fg-primary)', lineHeight: 1.2, marginTop: '4px' }}>Nodes</h1>
      </div>
      <div style={{ border: '1px solid var(--border)', borderRadius: '6px', overflow: 'hidden' }}>
        {nodes.map((n, i) => {
          const online = isOnline(n)
          const storageUsed = n.max_storage_bytes > 0 ? n.used_storage_bytes / n.max_storage_bytes : 0
          return (
            <div key={n.id} className="flex items-center gap-5 px-5 py-4" style={{ background: 'var(--bg-card)', borderBottom: i < nodes.length - 1 ? '1px solid var(--border)' : 'none' }}>
              <div style={{ width: '8px', height: '8px', borderRadius: '50%', background: online ? '#3fb950' : 'var(--fg-muted)', flexShrink: 0 }} className={online ? 'status-running' : ''} />
              <div className="flex-1">
                <div style={{ fontSize: '0.95rem', fontWeight: 600, color: 'var(--fg-primary)' }}>{n.hostname}</div>
                <div style={{ fontFamily: MONO, fontSize: '0.72rem', color: 'var(--fg-muted)', marginTop: '2px' }}>{n.role} · {online ? 'online' : 'offline'}</div>
              </div>
              {n.role === 'deploy' && n.max_storage_bytes > 0 && (
                <div className="w-32">
                  <div style={{ fontFamily: MONO, fontSize: '0.65rem', color: 'var(--fg-muted)', marginBottom: '4px', textAlign: 'right' }}>
                    {Math.round(storageUsed * 100)}%
                  </div>
                  <div style={{ height: '4px', background: 'var(--bg-hover)', borderRadius: '2px' }}>
                    <div style={{
                      height: '100%', borderRadius: '2px',
                      background: storageUsed > 0.9 ? 'var(--danger)' : storageUsed > 0.7 ? '#d29922' : '#3fb950',
                      width: `${storageUsed * 100}%`,
                      transition: 'width 300ms',
                    }} />
                  </div>
                </div>
              )}
            </div>
          )
        })}
        {nodes.length === 0 && (
          <div className="py-10 text-center" style={{ fontFamily: MONO, color: 'var(--fg-muted)', fontSize: '0.8rem', background: 'var(--bg-card)' }}>
            No nodes registered yet. Start an Agent to connect nodes.
          </div>
        )}
      </div>
    </div>
  )
}

export function UsersPage() {
  const [users, setUsers] = useState<import('../lib/types').User[]>([])
  const { user: me } = useAuth()
  useEffect(() => { api.users.list().then(setUsers).catch(() => {}) }, [])

  const toggleRole = async (u: import('../lib/types').User) => {
    const newRole = u.role === 'admin' ? 'member' : 'admin'
    await api.users.setRole(u.id, newRole)
    setUsers(prev => prev.map(x => x.id === u.id ? { ...x, role: newRole } : x))
  }

  return (
    <div className="page-enter">
      <div className="mb-8">
        <p style={{ fontFamily: MONO, color: 'var(--fg-muted)', fontSize: '0.7rem', letterSpacing: '0.05em' }}>ACCESS CONTROL</p>
        <h1 style={{ fontSize: '1.75rem', fontWeight: 700, color: 'var(--fg-primary)', lineHeight: 1.2, marginTop: '4px' }}>Users</h1>
      </div>
      <div style={{ border: '1px solid var(--border)', borderRadius: '6px', overflow: 'hidden' }}>
        <table className="w-full border-collapse">
          <thead>
            <tr style={{ borderBottom: '1px solid var(--border)', background: 'var(--bg-card)' }}>
              {['USER', 'EMAIL', 'ROLE', 'JOINED', ''].map(h => (
                <th key={h} style={{ fontFamily: MONO, fontSize: '0.65rem', color: 'var(--fg-muted)', letterSpacing: '0.08em', padding: '0.6rem 1rem', textAlign: 'left', fontWeight: 500 }}>
                  {h}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {users.map(u => (
              <tr key={u.id} style={{ borderBottom: '1px solid var(--border)', background: 'var(--bg-card)' }}
                onMouseEnter={e => { (e.currentTarget as HTMLTableRowElement).style.background = 'var(--bg-hover)' }}
                onMouseLeave={e => { (e.currentTarget as HTMLTableRowElement).style.background = 'var(--bg-card)' }}
              >
                <td style={{ padding: '0.7rem 1rem' }}>
                  <div className="flex items-center gap-2">
                    {u.avatar_url && <img src={u.avatar_url} alt="" className="rounded-full" style={{ width: '20px', height: '20px' }} />}
                    <span style={{ fontSize: '0.875rem', color: 'var(--fg-primary)', fontWeight: 500 }}>{u.name}</span>
                  </div>
                </td>
                <td style={{ padding: '0.7rem 1rem' }}>
                  <span style={{ fontFamily: MONO, fontSize: '0.75rem', color: 'var(--fg-muted)' }}>{u.email}</span>
                </td>
                <td style={{ padding: '0.7rem 1rem' }}>
                  <span style={{
                    fontFamily: MONO, fontSize: '0.7rem',
                    color: u.role === 'admin' ? 'var(--accent)' : 'var(--fg-muted)',
                    border: `1px solid ${u.role === 'admin' ? 'rgba(88,166,255,0.4)' : 'var(--border)'}`,
                    background: u.role === 'admin' ? 'rgba(88,166,255,0.1)' : 'transparent',
                    padding: '2px 8px', borderRadius: '2em',
                  }}>
                    {u.role}
                  </span>
                </td>
                <td style={{ padding: '0.7rem 1rem' }}>
                  <span style={{ fontFamily: MONO, fontSize: '0.75rem', color: 'var(--fg-muted)' }}>
                    {new Date(u.created_at).toLocaleDateString()}
                  </span>
                </td>
                <td style={{ padding: '0.7rem 1rem' }}>
                  {me?.role === 'admin' && me.id !== u.id && (
                    <button
                      onClick={() => toggleRole(u)}
                      style={{
                        fontFamily: MONO, fontSize: '0.72rem',
                        color: 'var(--fg-muted)',
                        background: 'var(--bg-hover)',
                        border: '1px solid var(--border)',
                        padding: '3px 10px', borderRadius: '6px',
                        cursor: 'pointer',
                      }}
                    >
                      {u.role === 'admin' ? 'Make Member' : 'Make Admin'}
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
