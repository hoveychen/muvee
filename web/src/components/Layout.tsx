import { ReactNode, useEffect, useState } from 'react'
import { NavLink, Outlet, useNavigate } from 'react-router-dom'
import { LayoutGrid, Database, Server, Users, LogOut, ChevronRight } from 'lucide-react'
import { useAuth } from '../lib/auth'
import { api } from '../lib/api'
import type { Node } from '../lib/types'

const navItems = [
  { to: '/projects', icon: LayoutGrid, label: 'PROJECTS' },
  { to: '/datasets', icon: Database, label: 'DATASETS' },
  { to: '/nodes', icon: Server, label: 'NODES' },
  { to: '/users', icon: Users, label: 'USERS' },
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
        <div className="px-6 py-7" style={{ borderBottom: '1px solid var(--border)' }}>
          <div style={{ fontFamily: 'Bebas Neue', fontSize: '2rem', color: 'var(--fg-primary)', letterSpacing: '0.05em' }}>
            MUVEE
          </div>
          <div style={{ fontFamily: 'DM Mono', fontSize: '0.6rem', color: 'var(--fg-muted)', letterSpacing: '0.15em' }}>
            PRIVATE CLOUD
          </div>
        </div>

        {/* Nav */}
        <nav className="flex-1 py-4">
          {navItems.map(({ to, icon: Icon, label }) => (
            <NavLink
              key={to}
              to={to}
              style={({ isActive }) => ({
                display: 'flex',
                alignItems: 'center',
                gap: '10px',
                padding: '10px 24px',
                fontFamily: 'DM Mono',
                fontSize: '0.72rem',
                letterSpacing: '0.1em',
                textDecoration: 'none',
                color: isActive ? 'var(--accent)' : 'var(--fg-muted)',
                background: isActive ? `var(--accent)11` : 'transparent',
                borderLeft: isActive ? '2px solid var(--accent)' : '2px solid transparent',
                transition: 'all 120ms',
              })}
            >
              <Icon size={14} />
              {label}
            </NavLink>
          ))}
        </nav>

        {/* User */}
        {user && (
          <div className="p-4" style={{ borderTop: '1px solid var(--border)' }}>
            <div className="flex items-center gap-3">
              {user.avatar_url ? (
                <img src={user.avatar_url} alt="" className="rounded-full" style={{ width: '28px', height: '28px' }} />
              ) : (
                <div className="rounded-full flex items-center justify-center" style={{ width: '28px', height: '28px', background: 'var(--bg-hover)', fontFamily: 'DM Mono', fontSize: '0.65rem', color: 'var(--fg-muted)' }}>
                  {user.name.charAt(0).toUpperCase()}
                </div>
              )}
              <div className="flex-1 min-w-0">
                <div style={{ fontFamily: 'DM Mono', fontSize: '0.7rem', color: 'var(--fg-primary)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {user.name || user.email.split('@')[0]}
                </div>
                <div style={{ fontFamily: 'DM Mono', fontSize: '0.6rem', color: 'var(--fg-muted)', letterSpacing: '0.08em' }}>
                  {user.role.toUpperCase()}
                </div>
              </div>
              <button
                onClick={handleLogout}
                title="Logout"
                style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--fg-muted)', padding: '4px' }}
                onMouseEnter={e => { (e.currentTarget as HTMLButtonElement).style.color = 'var(--danger)' }}
                onMouseLeave={e => { (e.currentTarget as HTMLButtonElement).style.color = 'var(--fg-muted)' }}
              >
                <LogOut size={13} />
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
        <p style={{ fontFamily: 'DM Mono', color: 'var(--fg-muted)', fontSize: '0.7rem', letterSpacing: '0.15em' }}>INFRASTRUCTURE</p>
        <h1 style={{ fontFamily: 'Bebas Neue', fontSize: '3rem', color: 'var(--fg-primary)', lineHeight: 1 }}>Nodes</h1>
      </div>
      <div className="space-y-px" style={{ background: 'var(--border)' }}>
        {nodes.map(n => {
          const online = isOnline(n)
          const storageUsed = n.max_storage_bytes > 0 ? n.used_storage_bytes / n.max_storage_bytes : 0
          return (
            <div key={n.id} className="flex items-center gap-5 px-6 py-5" style={{ background: 'var(--bg-card)' }}>
              <div style={{ width: '8px', height: '8px', borderRadius: '50%', background: online ? 'var(--accent)' : 'var(--fg-muted)', flexShrink: 0 }} className={online ? 'status-running' : ''} />
              <div className="flex-1">
                <div style={{ fontFamily: 'Bebas Neue', fontSize: '1.3rem', color: 'var(--fg-primary)' }}>{n.hostname}</div>
                <div style={{ fontFamily: 'DM Mono', fontSize: '0.68rem', color: 'var(--fg-muted)' }}>{n.role.toUpperCase()} · {online ? 'online' : 'offline'}</div>
              </div>
              {n.role === 'deploy' && n.max_storage_bytes > 0 && (
                <div className="w-32">
                  <div style={{ fontFamily: 'DM Mono', fontSize: '0.65rem', color: 'var(--fg-muted)', marginBottom: '4px', textAlign: 'right' }}>
                    {Math.round(storageUsed * 100)}%
                  </div>
                  <div style={{ height: '3px', background: 'var(--border)', borderRadius: '2px' }}>
                    <div style={{
                      height: '100%', borderRadius: '2px',
                      background: storageUsed > 0.9 ? 'var(--danger)' : storageUsed > 0.7 ? '#f0a03c' : 'var(--accent)',
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
          <div className="py-10 text-center" style={{ fontFamily: 'DM Mono', color: 'var(--fg-muted)', fontSize: '0.8rem' }}>
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
        <p style={{ fontFamily: 'DM Mono', color: 'var(--fg-muted)', fontSize: '0.7rem', letterSpacing: '0.15em' }}>ACCESS CONTROL</p>
        <h1 style={{ fontFamily: 'Bebas Neue', fontSize: '3rem', color: 'var(--fg-primary)', lineHeight: 1 }}>Users</h1>
      </div>
      <table className="w-full border-collapse">
        <thead>
          <tr style={{ borderBottom: '1px solid var(--border)' }}>
            {['USER', 'EMAIL', 'ROLE', 'JOINED', ''].map(h => (
              <th key={h} style={{ fontFamily: 'DM Mono', fontSize: '0.65rem', color: 'var(--fg-muted)', letterSpacing: '0.12em', padding: '0.5rem 1rem', textAlign: 'left', fontWeight: 500 }}>
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {users.map(u => (
            <tr key={u.id} style={{ borderBottom: '1px solid var(--border)' }}>
              <td style={{ padding: '0.8rem 1rem' }}>
                <div className="flex items-center gap-3">
                  {u.avatar_url && <img src={u.avatar_url} alt="" className="rounded-full" style={{ width: '24px', height: '24px' }} />}
                  <span style={{ fontFamily: 'Lora', fontSize: '0.9rem', color: 'var(--fg-primary)' }}>{u.name}</span>
                </div>
              </td>
              <td style={{ padding: '0.8rem 1rem' }}>
                <span style={{ fontFamily: 'DM Mono', fontSize: '0.72rem', color: 'var(--fg-muted)' }}>{u.email}</span>
              </td>
              <td style={{ padding: '0.8rem 1rem' }}>
                <span style={{
                  fontFamily: 'DM Mono', fontSize: '0.65rem',
                  color: u.role === 'admin' ? 'var(--accent)' : 'var(--fg-muted)',
                  border: `1px solid ${u.role === 'admin' ? 'var(--accent)' : 'var(--border)'}33`,
                  background: `${u.role === 'admin' ? 'var(--accent)' : 'var(--border)'}11`,
                  padding: '2px 8px', borderRadius: '2px',
                }}>
                  {u.role.toUpperCase()}
                </span>
              </td>
              <td style={{ padding: '0.8rem 1rem' }}>
                <span style={{ fontFamily: 'DM Mono', fontSize: '0.72rem', color: 'var(--fg-muted)' }}>
                  {new Date(u.created_at).toLocaleDateString()}
                </span>
              </td>
              <td style={{ padding: '0.8rem 1rem' }}>
                {me?.role === 'admin' && me.id !== u.id && (
                  <button
                    onClick={() => toggleRole(u)}
                    style={{
                      fontFamily: 'DM Mono', fontSize: '0.65rem',
                      color: 'var(--fg-muted)',
                      background: 'var(--bg-hover)',
                      border: '1px solid var(--border)',
                      padding: '3px 10px', borderRadius: '2px',
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
  )
}
