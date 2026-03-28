import { ReactNode, useEffect, useState } from 'react'
import { NavLink, Outlet, useNavigate } from 'react-router-dom'
import { LayoutGrid, Database, KeyRound, Server, Users, LogOut, Sun, Moon, Languages, Settings, CheckCircle, AlertCircle, XCircle, Copy, Check } from 'lucide-react'
import { useAuth } from '../lib/auth'
import { useTheme } from '../lib/theme'
import { useSettings } from '../lib/settings'
import { api } from '../lib/api'
import type { Node, NodeMetric, HealthCheck } from '../lib/types'
import { useTranslation } from 'react-i18next'

const MONO = 'var(--font-mono)'

export default function Layout({ children }: { children?: ReactNode }) {
  const { user } = useAuth()
  const navigate = useNavigate()
  const { theme, toggleTheme } = useTheme()
  const { t, i18n } = useTranslation()
  const { settings, loading } = useSettings()
  const isAdmin = user?.role === 'admin'

  // Redirect admin to onboarding if not yet completed
  useEffect(() => {
    if (!loading && isAdmin && settings.onboarded === 'false') {
      navigate('/onboard', { replace: true })
    }
  }, [loading, isAdmin, settings.onboarded, navigate])

  const ALL_NAV_ITEMS = [
    { to: '/projects', icon: LayoutGrid, label: t('nav.projects'), adminOnly: false },
    { to: '/datasets', icon: Database, label: t('nav.datasets'), adminOnly: false },
    { to: '/secrets', icon: KeyRound, label: t('nav.secrets'), adminOnly: false },
    { to: '/nodes', icon: Server, label: t('nav.nodes'), adminOnly: true },
    { to: '/users', icon: Users, label: t('nav.users'), adminOnly: true },
    { to: '/admin/settings', icon: Settings, label: t('nav.settings'), adminOnly: true },
  ]
  const navItems = ALL_NAV_ITEMS.filter(item => !item.adminOnly || isAdmin)

  const handleLogout = async () => {
    await fetch('/auth/logout', { method: 'POST', credentials: 'include' })
    navigate('/login')
  }

  const toggleLang = () => {
    i18n.changeLanguage(i18n.language === 'zh' ? 'en' : 'zh')
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
        <div className="px-4 py-4 flex items-center gap-3" style={{ borderBottom: '1px solid var(--border)' }}>
          <img
            src={settings.logo_url || '/icon.png'}
            alt={settings.site_name || 'muvee'}
            style={{ width: '28px', height: '28px', borderRadius: '6px', flexShrink: 0, objectFit: 'contain' }}
          />
          <div>
            <div style={{ fontFamily: MONO, fontSize: '0.9rem', fontWeight: 600, color: 'var(--fg-primary)' }}>
              {settings.site_name || 'muvee'}
            </div>
            <div style={{ fontFamily: MONO, fontSize: '0.65rem', color: 'var(--fg-muted)', marginTop: '1px' }}>
              {t('nav.privateCloud')}
            </div>
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
                <div style={{ fontFamily: MONO, fontSize: '0.62rem', color: 'var(--fg-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={user.email}>
                  {user.email}
                </div>
              </div>
              <button
                onClick={toggleLang}
                title={i18n.language === 'zh' ? t('nav.switchToEn') : t('nav.switchToZh')}
                style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--fg-muted)', padding: '4px', borderRadius: '4px' }}
                onMouseEnter={e => { (e.currentTarget as HTMLButtonElement).style.color = 'var(--fg-primary)' }}
                onMouseLeave={e => { (e.currentTarget as HTMLButtonElement).style.color = 'var(--fg-muted)' }}
              >
                <Languages size={14} />
              </button>
              <button
                onClick={toggleTheme}
                title={theme === 'dark' ? t('nav.switchToLight') : t('nav.switchToDark')}
                style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--fg-muted)', padding: '4px', borderRadius: '4px' }}
                onMouseEnter={e => { (e.currentTarget as HTMLButtonElement).style.color = 'var(--fg-primary)' }}
                onMouseLeave={e => { (e.currentTarget as HTMLButtonElement).style.color = 'var(--fg-muted)' }}
              >
                {theme === 'dark' ? <Sun size={14} /> : <Moon size={14} />}
              </button>
              <button
                onClick={handleLogout}
                title={t('nav.logout')}
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

function fmtBytes(bytes: number): string {
  if (bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.min(Math.floor(Math.log2(bytes) / 10), units.length - 1)
  return `${(bytes / Math.pow(1024, i)).toFixed(1)} ${units[i]}`
}

function MetricBar({ pct, label }: { pct: number; label: string }) {
  const color = pct > 90 ? 'var(--danger)' : pct > 70 ? '#d29922' : '#3fb950'
  return (
    <div style={{ minWidth: '100px' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', fontFamily: MONO, fontSize: '0.63rem', color: 'var(--fg-muted)', marginBottom: '3px' }}>
        <span>{label}</span>
        <span>{Math.round(pct)}%</span>
      </div>
      <div style={{ height: '3px', background: 'var(--bg-hover)', borderRadius: '2px' }}>
        <div style={{ height: '100%', borderRadius: '2px', background: color, width: `${Math.min(pct, 100)}%`, transition: 'width 300ms' }} />
      </div>
    </div>
  )
}

function parseNodeHealthReport(raw: string | null | undefined): HealthCheck[] | null {
  if (!raw) return null
  try {
    return JSON.parse(atob(raw)) as HealthCheck[]
  } catch {
    return null
  }
}

function NodeHintBlock({ hint }: { hint: string }) {
  const [copied, setCopied] = useState(false)
  const { t } = useTranslation()
  return (
    <div style={{
      marginTop: '4px', marginLeft: '18px', padding: '6px 8px', borderRadius: '4px',
      background: 'var(--bg-hover)', border: '1px solid var(--border)',
      display: 'flex', alignItems: 'flex-start', gap: '8px',
    }}>
      <pre style={{
        fontFamily: MONO, fontSize: '0.6rem', color: 'var(--fg-primary)',
        margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-all', lineHeight: 1.5, flex: 1,
      }}>{hint}</pre>
      <button
        onClick={() => { navigator.clipboard.writeText(hint); setCopied(true); setTimeout(() => setCopied(false), 2000) }}
        style={{
          background: 'none', border: '1px solid var(--border)', borderRadius: '4px',
          padding: '2px 6px', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: '3px',
          fontFamily: MONO, fontSize: '0.55rem', color: 'var(--fg-muted)', flexShrink: 0,
        }}
      >
        {copied ? <Check size={10} /> : <Copy size={10} />}
        {copied ? t('health.copied', 'Copied') : t('health.copy', 'Copy')}
      </button>
    </div>
  )
}

export function NodesPage() {
  const [nodes, setNodes] = useState<Node[]>([])
  const [metrics, setMetrics] = useState<Record<string, NodeMetric | null>>({})
  const { t } = useTranslation()

  useEffect(() => {
    api.nodes.list().then(ns => {
      setNodes(ns)
      ns.forEach(n => {
        api.nodes.metrics(n.id).then(m => {
          setMetrics(prev => ({ ...prev, [n.id]: m }))
        }).catch(() => {
          setMetrics(prev => ({ ...prev, [n.id]: null }))
        })
      })
    }).catch(() => {})
  }, [])

  const isOnline = (n: Node) => {
    const lastSeen = new Date(n.last_seen_at).getTime()
    return Date.now() - lastSeen < 2 * 60 * 1000
  }

  const offlineNodes = nodes.filter(n => !isOnline(n))

  const cleanOffline = async () => {
    if (!confirm(t('nodes.cleanConfirm'))) return
    await Promise.all(offlineNodes.map(n => api.nodes.delete(n.id)))
    setNodes(prev => prev.filter(n => isOnline(n)))
  }

  const hasBuilder = nodes.some(n => n.role === 'builder')
  const hasDeployer = nodes.some(n => n.role === 'deploy')

  return (
    <div className="page-enter">
      <div className="mb-8 flex items-center justify-between">
        <div>
          <p style={{ fontFamily: MONO, color: 'var(--fg-muted)', fontSize: '0.7rem', letterSpacing: '0.05em' }}>{t('nodes.sectionLabel')}</p>
          <h1 style={{ fontSize: '1.75rem', fontWeight: 700, color: 'var(--fg-primary)', lineHeight: 1.2, marginTop: '4px' }}>{t('nodes.heading')}</h1>
        </div>
        {offlineNodes.length > 0 && (
          <button onClick={cleanOffline} className="btn-secondary" style={{ fontFamily: MONO, fontSize: '0.75rem' }}>
            {t('nodes.cleanOffline')}
          </button>
        )}
      </div>
      {nodes.length > 0 && (!hasBuilder || !hasDeployer) && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: '8px', marginBottom: '16px' }}>
          {!hasBuilder && (
            <div style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '10px 14px', borderRadius: '6px', background: 'rgba(210, 153, 34, 0.1)', border: '1px solid rgba(210, 153, 34, 0.4)' }}>
              <AlertCircle size={16} color="#d29922" style={{ flexShrink: 0 }} />
              <span style={{ fontFamily: MONO, fontSize: '0.75rem', color: '#d29922' }}>{t('nodes.noBuilder')}</span>
            </div>
          )}
          {!hasDeployer && (
            <div style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '10px 14px', borderRadius: '6px', background: 'rgba(210, 153, 34, 0.1)', border: '1px solid rgba(210, 153, 34, 0.4)' }}>
              <AlertCircle size={16} color="#d29922" style={{ flexShrink: 0 }} />
              <span style={{ fontFamily: MONO, fontSize: '0.75rem', color: '#d29922' }}>{t('nodes.noDeployer')}</span>
            </div>
          )}
        </div>
      )}
      <div style={{ border: '1px solid var(--border)', borderRadius: '6px', overflow: 'hidden' }}>
        {nodes.map((n, i) => {
          const online = isOnline(n)
          const m = metrics[n.id]
          const cpuPct = m ? m.cpu_percent : 0
          const memPct = m && m.mem_total_bytes > 0 ? (m.mem_used_bytes / m.mem_total_bytes) * 100 : 0
          const diskPct = m && m.disk_total_bytes > 0 ? (m.disk_used_bytes / m.disk_total_bytes) * 100 : 0
          const healthChecks = parseNodeHealthReport(n.health_report)
          const hasIssues = healthChecks?.some(c => c.status !== 'ok')
          return (
            <div key={n.id} style={{ background: 'var(--bg-card)', borderBottom: i < nodes.length - 1 ? '1px solid var(--border)' : 'none', padding: '1rem 1.25rem' }}>
              <div className="flex items-center gap-4">
                <div style={{ width: '8px', height: '8px', borderRadius: '50%', background: online ? '#3fb950' : 'var(--fg-muted)', flexShrink: 0 }} className={online ? 'status-running' : ''} />
                <div style={{ flex: 1 }}>
                  <div style={{ fontSize: '0.95rem', fontWeight: 600, color: 'var(--fg-primary)' }}>{n.hostname}</div>
                  <div style={{ fontFamily: MONO, fontSize: '0.72rem', color: 'var(--fg-muted)', marginTop: '2px' }}>
                    {n.role} · {online ? t('nodes.online') : t('nodes.offline')}
                    {m && (
                      <span style={{ marginLeft: '8px' }}>
                        · {t('nodes.load')}: {m.load1.toFixed(2)} / {m.load5.toFixed(2)} / {m.load15.toFixed(2)}
                      </span>
                    )}
                  </div>
                </div>
                {m && (
                  <div style={{ display: 'flex', gap: '16px', alignItems: 'center' }}>
                    <MetricBar pct={cpuPct} label={t('nodes.cpu')} />
                    <MetricBar pct={memPct} label={t('nodes.mem')} />
                    <MetricBar pct={diskPct} label={t('nodes.disk')} />
                  </div>
                )}
              </div>
              {m && (
                <div style={{ display: 'flex', gap: '24px', marginTop: '8px', paddingLeft: '20px' }}>
                  <div style={{ fontFamily: MONO, fontSize: '0.65rem', color: 'var(--fg-muted)' }}>
                    <span style={{ color: 'var(--fg-primary)', fontWeight: 500 }}>{t('nodes.cpu')}</span>{' '}
                    {cpuPct.toFixed(1)}%
                  </div>
                  <div style={{ fontFamily: MONO, fontSize: '0.65rem', color: 'var(--fg-muted)' }}>
                    <span style={{ color: 'var(--fg-primary)', fontWeight: 500 }}>{t('nodes.mem')}</span>{' '}
                    {fmtBytes(m.mem_used_bytes)} / {fmtBytes(m.mem_total_bytes)}
                  </div>
                  <div style={{ fontFamily: MONO, fontSize: '0.65rem', color: 'var(--fg-muted)' }}>
                    <span style={{ color: 'var(--fg-primary)', fontWeight: 500 }}>{t('nodes.disk')}</span>{' '}
                    {fmtBytes(m.disk_used_bytes)} / {fmtBytes(m.disk_total_bytes)}
                  </div>
                  <div style={{ fontFamily: MONO, fontSize: '0.65rem', color: 'var(--fg-muted)' }}>
                    <span style={{ color: 'var(--fg-primary)', fontWeight: 500 }}>{t('nodes.updated')}</span>{' '}
                    {new Date(m.collected_at * 1000).toLocaleTimeString()}
                  </div>
                </div>
              )}
              {healthChecks && (
                <div style={{ marginTop: '10px', paddingLeft: '20px' }}>
                  {hasIssues ? (
                    <div style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
                      {healthChecks.filter(c => c.status !== 'ok').map(c => {
                        const color = c.status === 'warning' ? '#d29922' : '#f85149'
                        const icon = c.status === 'warning'
                          ? <AlertCircle size={12} color={color} style={{ flexShrink: 0, marginTop: '1px' }} />
                          : <XCircle size={12} color={color} style={{ flexShrink: 0, marginTop: '1px' }} />
                        return (
                          <div key={c.name}>
                            <div style={{ display: 'flex', alignItems: 'flex-start', gap: '6px', fontFamily: MONO, fontSize: '0.67rem' }}>
                              {icon}
                              <span style={{ color, fontWeight: 600 }}>{c.name}</span>
                              <span style={{ color: 'var(--fg-muted)' }}>{c.message}</span>
                            </div>
                            {c.hint && <NodeHintBlock hint={c.hint} />}
                          </div>
                        )
                      })}
                    </div>
                  ) : (
                    <div style={{ display: 'flex', alignItems: 'center', gap: '5px', fontFamily: MONO, fontSize: '0.67rem', color: 'var(--fg-muted)' }}>
                      <CheckCircle size={12} color="#3fb950" />
                      all checks passed
                    </div>
                  )}
                </div>
              )}
            </div>
          )
        })}
        {nodes.length === 0 && (
          <div className="py-10 text-center" style={{ fontFamily: MONO, color: 'var(--fg-muted)', fontSize: '0.8rem', background: 'var(--bg-card)' }}>
            {t('nodes.empty')}
          </div>
        )}
      </div>
    </div>
  )
}

export function UsersPage() {
  const [users, setUsers] = useState<import('../lib/types').User[]>([])
  const { user: me } = useAuth()
  const { t } = useTranslation()
  useEffect(() => { api.users.list().then(setUsers).catch(() => {}) }, [])

  const toggleRole = async (u: import('../lib/types').User) => {
    const newRole = u.role === 'admin' ? 'member' : 'admin'
    await api.users.setRole(u.id, newRole)
    setUsers(prev => prev.map(x => x.id === u.id ? { ...x, role: newRole } : x))
  }

  return (
    <div className="page-enter">
      <div className="mb-8">
        <p style={{ fontFamily: MONO, color: 'var(--fg-muted)', fontSize: '0.7rem', letterSpacing: '0.05em' }}>{t('users.sectionLabel')}</p>
        <h1 style={{ fontSize: '1.75rem', fontWeight: 700, color: 'var(--fg-primary)', lineHeight: 1.2, marginTop: '4px' }}>{t('users.heading')}</h1>
      </div>
      <div style={{ border: '1px solid var(--border)', borderRadius: '6px', overflow: 'hidden' }}>
        <table className="w-full border-collapse">
          <thead>
            <tr style={{ borderBottom: '1px solid var(--border)', background: 'var(--bg-card)' }}>
              {[t('users.columns.user'), t('users.columns.email'), t('users.columns.role'), t('users.columns.joined'), ''].map(h => (
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
                      {u.role === 'admin' ? t('users.makeMember') : t('users.makeAdmin')}
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
