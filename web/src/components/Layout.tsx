import { ReactNode, useEffect, useState } from 'react'
import { NavLink, Outlet, useNavigate } from 'react-router-dom'
import { Home, LayoutGrid, Database, KeyRound, Key, Server, Users, LogOut, Sun, Moon, Languages, Settings, CheckCircle, AlertCircle, XCircle, Copy, Check, Globe, ShieldAlert, Clock, Loader, Mail, Link2, Trash2, Plus } from 'lucide-react'
import { useAuth } from '../lib/auth'
import { useTheme } from '../lib/theme'
import { useSettings } from '../lib/settings'
import { api } from '../lib/api'
import type { Node, NodeMetric, HealthCheck, RuntimeConfig, AuthorizationStatus } from '../lib/types'
import { useTranslation } from 'react-i18next'

const MONO = 'var(--font-mono)'

export default function Layout({ children }: { children?: ReactNode }) {
  const { user } = useAuth()
  const navigate = useNavigate()
  const { theme, toggleTheme } = useTheme()
  const { t, i18n } = useTranslation()
  const { settings, loading } = useSettings()
  const isAdmin = user?.role === 'admin'
  const [runtimeConfig, setRuntimeConfig] = useState<RuntimeConfig | null>(null)
  const [authzStatus, setAuthzStatus] = useState<AuthorizationStatus | null>(null)
  const [requestingAccess, setRequestingAccess] = useState(false)

  // Whether the user is effectively authorized (admin, open mode, or approved).
  // authzStatus.authorized is the canonical answer once the API has responded;
  // before that, optimistically allow rendering to avoid a flash of "no access".
  const isAuthorized = isAdmin || authzStatus === null || authzStatus.authorized

  // Redirect admin to onboarding if not yet completed
  useEffect(() => {
    if (!loading && isAdmin && settings.onboarded === 'false') {
      navigate('/onboard', { replace: true })
    }
  }, [loading, isAdmin, settings.onboarded, navigate])

  useEffect(() => {
    api.runtime.config().then(setRuntimeConfig).catch(() => {})
  }, [])

  useEffect(() => {
    if (user) {
      api.authorization.status().then(setAuthzStatus).catch(() => {})
    }
  }, [user])

  const handleRequestAccess = async () => {
    setRequestingAccess(true)
    try {
      await api.authorization.request()
      const status = await api.authorization.status()
      setAuthzStatus(status)
    } catch {
      // ignore
    } finally {
      setRequestingAccess(false)
    }
  }

  const ALL_NAV_ITEMS = [
    { to: '/portal', icon: Home, label: t('nav.portal'), adminOnly: false, hidden: false, requireAuth: false },
    { to: '/projects', icon: LayoutGrid, label: t('nav.projects'), adminOnly: false, hidden: false, requireAuth: true },
    { to: '/datasets', icon: Database, label: t('nav.datasets'), adminOnly: false, hidden: false, requireAuth: true },
    { to: '/secrets', icon: KeyRound, label: t('nav.secrets'), adminOnly: false, hidden: runtimeConfig !== null && !runtimeConfig.secrets_enabled, requireAuth: true },
    { to: '/settings/tokens', icon: Key, label: t('nav.tokens'), adminOnly: false, hidden: false, requireAuth: true },
    { to: '/nodes', icon: Server, label: t('nav.nodes'), adminOnly: true, hidden: false, requireAuth: false },
    { to: '/tunnels', icon: Globe, label: t('nav.tunnels'), adminOnly: true, hidden: false, requireAuth: false },
    { to: '/users', icon: Users, label: t('nav.users'), adminOnly: true, hidden: false, requireAuth: false },
    { to: '/admin/settings', icon: Settings, label: t('nav.settings'), adminOnly: true, hidden: false, requireAuth: false },
  ]

  const userNavItems = ALL_NAV_ITEMS.filter(item => !item.adminOnly && !item.hidden && (isAuthorized || !item.requireAuth))
  const adminNavItems = ALL_NAV_ITEMS.filter(item => item.adminOnly && !item.hidden)

  const handleLogout = async () => {
    await fetch('/auth/logout', { method: 'POST', credentials: 'include' })
    navigate('/login')
  }

  const toggleLang = () => {
    i18n.changeLanguage(i18n.language === 'zh' ? 'en' : 'zh')
  }

  const navLinkStyle = ({ isActive }: { isActive: boolean }) => ({
    display: 'flex',
    alignItems: 'center',
    gap: '10px',
    padding: '8px 16px',
    margin: '2px 8px',
    borderRadius: '6px',
    fontSize: '0.875rem',
    textDecoration: 'none' as const,
    color: isActive ? 'var(--sidebar-fg-active)' : 'var(--sidebar-fg)',
    background: isActive ? 'var(--sidebar-hover)' : 'transparent',
    fontWeight: isActive ? 500 : 400,
    transition: 'all 120ms',
  })

  return (
    <div className="flex min-h-screen" style={{ background: 'var(--bg-base)' }}>
      {/* Sidebar */}
      <aside
        className="flex flex-col w-60 flex-shrink-0"
        style={{
          background: 'var(--sidebar-bg)',
          position: 'sticky',
          top: 0,
          height: '100vh',
        }}
      >
        {/* Logo */}
        <div className="px-5 py-5 flex items-center gap-3" style={{ borderBottom: '1px solid var(--sidebar-border)' }}>
          <img
            src={settings.logo_url || '/icon.png'}
            alt={settings.site_name || 'muvee'}
            style={{ width: '32px', height: '32px', borderRadius: '6px', flexShrink: 0, objectFit: 'contain' }}
          />
          <div>
            <div style={{ fontSize: '0.9375rem', fontWeight: 600, color: 'var(--sidebar-fg-active)' }}>
              {settings.site_name || 'muvee'}
            </div>
            <div style={{ fontSize: '0.6875rem', color: 'var(--sidebar-fg)', marginTop: '1px', opacity: 0.7 }}>
              {t('nav.privateCloud')}
            </div>
          </div>
        </div>

        {/* Nav */}
        <nav className="flex-1 py-3 overflow-y-auto">
          {/* Main section */}
          <div style={{ padding: '4px 20px 6px', fontSize: '0.6875rem', fontWeight: 600, color: 'var(--sidebar-fg)', opacity: 0.5, textTransform: 'uppercase', letterSpacing: '0.06em' }}>
            {t('nav.menu', 'Menu')}
          </div>
          {userNavItems.map(({ to, icon: Icon, label }) => (
            <NavLink key={to} to={to} style={navLinkStyle}>
              <Icon size={18} />
              {label}
            </NavLink>
          ))}

          {/* Admin section */}
          {isAdmin && adminNavItems.length > 0 && (
            <>
              <div style={{ padding: '16px 20px 6px', fontSize: '0.6875rem', fontWeight: 600, color: 'var(--sidebar-fg)', opacity: 0.5, textTransform: 'uppercase', letterSpacing: '0.06em' }}>
                {t('nav.admin', 'Administration')}
              </div>
              {adminNavItems.map(({ to, icon: Icon, label }) => (
                <NavLink key={to} to={to} style={navLinkStyle}>
                  <Icon size={18} />
                  {label}
                </NavLink>
              ))}
            </>
          )}
        </nav>

        {/* Authorization status banner for unauthorized users */}
        {!isAuthorized && authzStatus && (
          <div style={{ padding: '12px 16px', borderTop: '1px solid var(--sidebar-border)', background: 'var(--sidebar-hover)' }}>
            {authzStatus.request?.status === 'pending' ? (
              <div style={{ display: 'flex', alignItems: 'center', gap: '8px', fontSize: '0.75rem', color: 'var(--sidebar-fg)' }}>
                <Clock size={14} style={{ flexShrink: 0 }} />
                <span>{t('authorization.pendingBanner')}</span>
              </div>
            ) : (
              <>
                <div style={{ display: 'flex', alignItems: 'center', gap: '8px', fontSize: '0.75rem', color: 'var(--sidebar-fg)', marginBottom: '8px' }}>
                  <ShieldAlert size={14} style={{ flexShrink: 0 }} />
                  <span>{t('authorization.requiredBanner')}</span>
                </div>
                <button
                  onClick={handleRequestAccess}
                  disabled={requestingAccess}
                  style={{
                    width: '100%', padding: '6px 12px',
                    background: 'var(--accent)', color: '#fff', border: 'none', borderRadius: '6px',
                    fontFamily: MONO, fontSize: '0.75rem', fontWeight: 600,
                    cursor: requestingAccess ? 'default' : 'pointer',
                  }}
                >
                  {requestingAccess ? t('authorization.requesting') : t('authorization.requestAccess')}
                </button>
              </>
            )}
          </div>
        )}

        {/* User */}
        {user && (
          <div className="p-4" style={{ borderTop: '1px solid var(--sidebar-border)' }}>
            <NavLink
              to="/settings/profile"
              title={t('settingsProfile.editTooltip', 'Edit profile')}
              className="flex items-center gap-3"
              style={{
                textDecoration: 'none',
                padding: '4px',
                margin: '-4px',
                borderRadius: '6px',
                transition: 'background 120ms',
              }}
              onMouseEnter={e => { (e.currentTarget as HTMLAnchorElement).style.background = 'var(--sidebar-hover)' }}
              onMouseLeave={e => { (e.currentTarget as HTMLAnchorElement).style.background = 'transparent' }}
            >
              {user.avatar_url ? (
                <img src={user.avatar_url} alt="" className="rounded-full" style={{ width: '32px', height: '32px' }} />
              ) : (
                <div className="rounded-full flex items-center justify-center" style={{ width: '32px', height: '32px', background: 'var(--sidebar-hover)', fontSize: '0.75rem', color: 'var(--sidebar-fg-active)', fontWeight: 600 }}>
                  {(user.name || user.email || '?').charAt(0).toUpperCase()}
                </div>
              )}
              <div className="flex-1 min-w-0">
                <div style={{ fontSize: '0.8125rem', color: 'var(--sidebar-fg-active)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontWeight: 500 }}>
                  {user.name || user.email.split('@')[0]}
                </div>
                <div style={{ fontSize: '0.6875rem', color: 'var(--sidebar-fg)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', opacity: 0.7 }} title={user.email}>
                  {user.email}
                </div>
              </div>
            </NavLink>
            <div className="flex items-center gap-1 mt-3">
              <button
                onClick={toggleLang}
                title={i18n.language === 'zh' ? t('nav.switchToEn') : t('nav.switchToZh')}
                style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--sidebar-fg)', padding: '6px', borderRadius: '4px', transition: 'color 120ms' }}
                onMouseEnter={e => { (e.currentTarget as HTMLButtonElement).style.color = 'var(--sidebar-fg-active)' }}
                onMouseLeave={e => { (e.currentTarget as HTMLButtonElement).style.color = 'var(--sidebar-fg)' }}
              >
                <Languages size={16} />
              </button>
              <button
                onClick={toggleTheme}
                title={theme === 'dark' ? t('nav.switchToLight') : t('nav.switchToDark')}
                style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--sidebar-fg)', padding: '6px', borderRadius: '4px', transition: 'color 120ms' }}
                onMouseEnter={e => { (e.currentTarget as HTMLButtonElement).style.color = 'var(--sidebar-fg-active)' }}
                onMouseLeave={e => { (e.currentTarget as HTMLButtonElement).style.color = 'var(--sidebar-fg)' }}
              >
                {theme === 'dark' ? <Sun size={16} /> : <Moon size={16} />}
              </button>
              <div style={{ flex: 1 }} />
              <button
                onClick={handleLogout}
                title={t('nav.logout')}
                style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--sidebar-fg)', padding: '6px', borderRadius: '4px', transition: 'color 120ms' }}
                onMouseEnter={e => { (e.currentTarget as HTMLButtonElement).style.color = '#ef4444' }}
                onMouseLeave={e => { (e.currentTarget as HTMLButtonElement).style.color = 'var(--sidebar-fg)' }}
              >
                <LogOut size={16} />
              </button>
            </div>
          </div>
        )}
      </aside>

      {/* Main content */}
      <main className="flex-1 min-w-0 p-8" style={{ maxWidth: '1200px' }}>
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
  const color = pct > 90 ? 'var(--danger)' : pct > 70 ? 'var(--warning)' : 'var(--success)'
  return (
    <div style={{ minWidth: '110px' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: '0.75rem', color: 'var(--fg-muted)', marginBottom: '4px' }}>
        <span>{label}</span>
        <span style={{ fontFamily: MONO }}>{Math.round(pct)}%</span>
      </div>
      <div style={{ height: '4px', background: 'var(--bg-hover)', borderRadius: '2px' }}>
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
      marginTop: '6px', marginLeft: '22px', padding: '8px 10px', borderRadius: '6px',
      background: 'var(--bg-hover)', border: '1px solid var(--border)',
      display: 'flex', alignItems: 'flex-start', gap: '8px',
    }}>
      <pre style={{
        fontFamily: MONO, fontSize: '0.75rem', color: 'var(--fg-primary)',
        margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-all', lineHeight: 1.5, flex: 1,
      }}>{hint}</pre>
      <button
        onClick={() => { navigator.clipboard.writeText(hint); setCopied(true); setTimeout(() => setCopied(false), 2000) }}
        className="btn-secondary"
        style={{ padding: '2px 8px', fontSize: '0.75rem', flexShrink: 0 }}
      >
        {copied ? <Check size={12} /> : <Copy size={12} />}
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
      <div className="page-header flex items-center justify-between">
        <div>
          <h1 className="page-title">{t('nodes.heading')}</h1>
          <p className="page-subtitle">{t('nodes.sectionLabel')}</p>
        </div>
        {offlineNodes.length > 0 && (
          <button onClick={cleanOffline} className="btn-secondary">
            {t('nodes.cleanOffline')}
          </button>
        )}
      </div>

      {nodes.length > 0 && (!hasBuilder || !hasDeployer) && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: '8px', marginBottom: '16px' }}>
          {!hasBuilder && (
            <div className="badge-warning" style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '10px 14px', borderRadius: '6px', fontSize: '0.8125rem' }}>
              <AlertCircle size={16} style={{ flexShrink: 0 }} />
              <span>{t('nodes.noBuilder')}</span>
            </div>
          )}
          {!hasDeployer && (
            <div className="badge-warning" style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '10px 14px', borderRadius: '6px', fontSize: '0.8125rem' }}>
              <AlertCircle size={16} style={{ flexShrink: 0 }} />
              <span>{t('nodes.noDeployer')}</span>
            </div>
          )}
        </div>
      )}

      <div className="card">
        {nodes.map((n, i) => {
          const online = isOnline(n)
          const m = metrics[n.id]
          const cpuPct = m ? m.cpu_percent : 0
          const memPct = m && m.mem_total_bytes > 0 ? (m.mem_used_bytes / m.mem_total_bytes) * 100 : 0
          const diskPct = m && m.disk_total_bytes > 0 ? (m.disk_used_bytes / m.disk_total_bytes) * 100 : 0
          const healthChecks = parseNodeHealthReport(n.health_report)
          const hasIssues = healthChecks?.some(c => c.status !== 'ok')
          return (
            <div key={n.id} style={{ borderBottom: i < nodes.length - 1 ? '1px solid var(--border)' : 'none', padding: '16px 20px' }}>
              <div className="flex items-center gap-4">
                <div style={{ width: '10px', height: '10px', borderRadius: '50%', background: online ? 'var(--success)' : 'var(--fg-muted)', flexShrink: 0 }} className={online ? 'status-running' : ''} />
                <div style={{ flex: 1 }}>
                  <div style={{ fontSize: '0.9375rem', fontWeight: 600, color: 'var(--fg-primary)' }}>{n.hostname}</div>
                  <div style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginTop: '2px' }}>
                    <span className="badge-neutral" style={{ fontSize: '0.75rem', marginRight: '8px' }}>{n.role}</span>
                    {online ? t('nodes.online') : t('nodes.offline')}
                    {m && (
                      <span style={{ marginLeft: '12px' }}>
                        · {t('nodes.load')}: <span style={{ fontFamily: MONO }}>{m.load1.toFixed(2)} / {m.load5.toFixed(2)} / {m.load15.toFixed(2)}</span>
                      </span>
                    )}
                  </div>
                </div>
                {m && (
                  <div style={{ display: 'flex', gap: '20px', alignItems: 'center' }}>
                    <MetricBar pct={cpuPct} label={t('nodes.cpu')} />
                    <MetricBar pct={memPct} label={t('nodes.mem')} />
                    <MetricBar pct={diskPct} label={t('nodes.disk')} />
                  </div>
                )}
              </div>
              {m && (
                <div style={{ display: 'flex', gap: '24px', marginTop: '10px', paddingLeft: '26px' }}>
                  <div style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>
                    <span style={{ fontWeight: 500, color: 'var(--fg-primary)' }}>{t('nodes.cpu')}</span>{' '}
                    <span style={{ fontFamily: MONO }}>{cpuPct.toFixed(1)}%</span>
                  </div>
                  <div style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>
                    <span style={{ fontWeight: 500, color: 'var(--fg-primary)' }}>{t('nodes.mem')}</span>{' '}
                    <span style={{ fontFamily: MONO }}>{fmtBytes(m.mem_used_bytes)} / {fmtBytes(m.mem_total_bytes)}</span>
                  </div>
                  <div style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>
                    <span style={{ fontWeight: 500, color: 'var(--fg-primary)' }}>{t('nodes.disk')}</span>{' '}
                    <span style={{ fontFamily: MONO }}>{fmtBytes(m.disk_used_bytes)} / {fmtBytes(m.disk_total_bytes)}</span>
                  </div>
                  <div style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>
                    <span style={{ fontWeight: 500, color: 'var(--fg-primary)' }}>{t('nodes.updated')}</span>{' '}
                    {new Date(m.collected_at * 1000).toLocaleTimeString()}
                  </div>
                </div>
              )}
              {healthChecks && (
                <div style={{ marginTop: '10px', paddingLeft: '26px' }}>
                  {hasIssues ? (
                    <div style={{ display: 'flex', flexDirection: 'column', gap: '6px' }}>
                      {healthChecks.filter(c => c.status !== 'ok').map(c => {
                        const color = c.status === 'warning' ? 'var(--warning)' : 'var(--danger)'
                        const icon = c.status === 'warning'
                          ? <AlertCircle size={14} color={color} style={{ flexShrink: 0, marginTop: '1px' }} />
                          : <XCircle size={14} color={color} style={{ flexShrink: 0, marginTop: '1px' }} />
                        return (
                          <div key={c.name}>
                            <div style={{ display: 'flex', alignItems: 'flex-start', gap: '6px', fontSize: '0.8125rem' }}>
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
                    <div style={{ display: 'flex', alignItems: 'center', gap: '6px', fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>
                      <CheckCircle size={14} color="var(--success)" />
                      all checks passed
                    </div>
                  )}
                </div>
              )}
            </div>
          )
        })}
        {nodes.length === 0 && (
          <div className="py-12 text-center" style={{ color: 'var(--fg-muted)', fontSize: '0.875rem' }}>
            {t('nodes.empty')}
          </div>
        )}
      </div>
    </div>
  )
}

export function UsersPage() {
  const [users, setUsers] = useState<import('../lib/types').User[]>([])
  const [pendingRequests, setPendingRequests] = useState<import('../lib/types').AuthorizationRequest[]>([])
  const [invitations, setInvitations] = useState<import('../lib/types').Invitation[]>([])
  const [inviteLinks, setInviteLinks] = useState<import('../lib/types').InvitationLink[]>([])
  const [inviteEmail, setInviteEmail] = useState('')
  const [inviteEmailSaving, setInviteEmailSaving] = useState(false)
  const [linkExpiresDays, setLinkExpiresDays] = useState<string>('7')
  const [linkSaving, setLinkSaving] = useState(false)
  const [freshLinkToken, setFreshLinkToken] = useState<{ id: string; token: string } | null>(null)
  const [copiedLinkId, setCopiedLinkId] = useState<string | null>(null)
  const { user: me } = useAuth()
  const { settings } = useSettings()
  const { t } = useTranslation()
  const accessMode = settings.access_mode || 'open'
  const isRequestMode = accessMode === 'request'
  const isInviteMode = accessMode === 'invite'

  useEffect(() => { api.users.list().then(setUsers).catch(() => {}) }, [])
  useEffect(() => {
    if (isRequestMode) {
      api.admin.listAuthorizationRequests().then(setPendingRequests).catch(() => {})
    } else {
      setPendingRequests([])
    }
  }, [isRequestMode])
  useEffect(() => {
    if (isInviteMode) {
      api.admin.listInvitations().then(setInvitations).catch(() => {})
      api.admin.listInvitationLinks().then(setInviteLinks).catch(() => {})
    } else {
      setInvitations([])
      setInviteLinks([])
    }
  }, [isInviteMode])

  const toggleRole = async (u: import('../lib/types').User) => {
    const newRole = u.role === 'admin' ? 'member' : 'admin'
    await api.users.setRole(u.id, newRole)
    setUsers(prev => prev.map(x => x.id === u.id ? { ...x, role: newRole } : x))
  }

  const handleApprove = async (reqId: string) => {
    await api.admin.approveAuthorization(reqId)
    setPendingRequests(prev => prev.filter(r => r.id !== reqId))
    // Refresh user list to reflect the updated authorized status
    api.users.list().then(setUsers).catch(() => {})
  }

  const handleReject = async (reqId: string) => {
    await api.admin.rejectAuthorization(reqId)
    setPendingRequests(prev => prev.filter(r => r.id !== reqId))
  }

  const handleAddInvitation = async () => {
    const email = inviteEmail.trim()
    if (!email) return
    setInviteEmailSaving(true)
    try {
      const inv = await api.admin.createInvitation(email)
      setInvitations(prev => [inv, ...prev])
      setInviteEmail('')
    } catch {
      // ignore
    } finally {
      setInviteEmailSaving(false)
    }
  }

  const handleDeleteInvitation = async (id: string) => {
    await api.admin.deleteInvitation(id)
    setInvitations(prev => prev.filter(i => i.id !== id))
  }

  const handleCreateInviteLink = async () => {
    setLinkSaving(true)
    try {
      const days = parseInt(linkExpiresDays, 10)
      const payload: { expires_in_days?: number } = {}
      if (Number.isFinite(days) && days > 0) payload.expires_in_days = days
      const link = await api.admin.createInvitationLink(payload)
      setInviteLinks(prev => [link, ...prev])
      if (link.token) setFreshLinkToken({ id: link.id, token: link.token })
    } catch {
      // ignore
    } finally {
      setLinkSaving(false)
    }
  }

  const handleDeleteInviteLink = async (id: string) => {
    await api.admin.deleteInvitationLink(id)
    setInviteLinks(prev => prev.filter(l => l.id !== id))
    if (freshLinkToken?.id === id) setFreshLinkToken(null)
  }

  const buildInviteUrl = (token: string) => {
    const base = window.location.origin
    return `${base}/login?invite_token=${encodeURIComponent(token)}`
  }

  const copyInviteUrl = async (id: string, token: string) => {
    try {
      await navigator.clipboard.writeText(buildInviteUrl(token))
      setCopiedLinkId(id)
      setTimeout(() => setCopiedLinkId(prev => (prev === id ? null : prev)), 2000)
    } catch {
      // ignore
    }
  }

  return (
    <div className="page-enter">
      <div className="page-header">
        <h1 className="page-title">{t('users.heading')}</h1>
        <p className="page-subtitle">{t('users.sectionLabel')}</p>
      </div>

      {/* Pending authorization requests (request mode only) */}
      {isRequestMode && pendingRequests.length > 0 && (
        <div className="card" style={{ marginBottom: '24px' }}>
          <div className="card-header" style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
            <AlertCircle size={14} color="var(--warning)" />
            {t('users.pendingRequests', { count: pendingRequests.length })}
          </div>
          <div>
            {pendingRequests.map((req, i) => (
              <div
                key={req.id}
                className="flex items-center gap-4 px-5 py-3"
                style={{ borderBottom: i < pendingRequests.length - 1 ? '1px solid var(--border)' : 'none' }}
              >
                <div className="flex items-center gap-3 flex-1">
                  {req.user_avatar_url && (
                    <img src={req.user_avatar_url} alt="" className="rounded-full" style={{ width: '28px', height: '28px' }} />
                  )}
                  <div>
                    <div style={{ fontWeight: 500, fontSize: '0.875rem', color: 'var(--fg-primary)' }}>{req.user_name}</div>
                    <div style={{ fontSize: '0.75rem', color: 'var(--fg-muted)' }}>{req.user_email}</div>
                  </div>
                </div>
                <span style={{ fontSize: '0.75rem', color: 'var(--fg-muted)' }}>
                  {new Date(req.created_at).toLocaleDateString()}
                </span>
                <div className="flex items-center gap-2">
                  <button
                    onClick={() => handleApprove(req.id)}
                    className="btn-primary"
                    style={{ padding: '4px 12px', fontSize: '0.8125rem' }}
                  >
                    {t('users.approve')}
                  </button>
                  <button
                    onClick={() => handleReject(req.id)}
                    className="btn-secondary"
                    style={{ padding: '4px 12px', fontSize: '0.8125rem', color: 'var(--danger)', borderColor: 'var(--danger)' }}
                  >
                    {t('users.reject')}
                  </button>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Invitations management (invite mode only) */}
      {isInviteMode && (
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '16px', marginBottom: '24px' }}>
          {/* Email white-list */}
          <div className="card">
            <div className="card-header" style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
              <Mail size={14} />
              {t('users.invitations.emailListTitle')}
            </div>
            <div style={{ padding: '16px' }}>
              <p style={{ fontSize: '0.75rem', color: 'var(--fg-muted)', marginBottom: '12px', lineHeight: 1.5 }}>
                {t('users.invitations.emailListHint')}
              </p>
              <div style={{ display: 'flex', gap: '6px', marginBottom: '12px' }}>
                <input
                  type="email"
                  className="form-input"
                  placeholder={t('users.invitations.emailPlaceholder')}
                  value={inviteEmail}
                  onChange={e => setInviteEmail(e.target.value)}
                  onKeyDown={e => { if (e.key === 'Enter') handleAddInvitation() }}
                  style={{ flex: 1 }}
                />
                <button
                  onClick={handleAddInvitation}
                  disabled={inviteEmailSaving || !inviteEmail.trim()}
                  className="btn-primary"
                  style={{ padding: '6px 12px', fontSize: '0.8125rem' }}
                >
                  {inviteEmailSaving ? <Loader size={12} style={{ animation: 'spin 1s linear infinite' }} /> : <Plus size={12} />}
                  {t('users.invitations.addEmail')}
                </button>
              </div>
              {invitations.length === 0 ? (
                <div style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', padding: '12px 0', textAlign: 'center' }}>
                  {t('users.invitations.emailEmpty')}
                </div>
              ) : (
                <div style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
                  {invitations.map(inv => (
                    <div
                      key={inv.id}
                      className="flex items-center gap-3"
                      style={{ padding: '6px 10px', borderRadius: '6px', background: 'var(--bg-base)' }}
                    >
                      <span style={{ flex: 1, fontSize: '0.8125rem', color: 'var(--fg-primary)', wordBreak: 'break-all' }}>
                        {inv.email}
                      </span>
                      <span style={{ fontSize: '0.75rem', color: 'var(--fg-muted)' }}>
                        {new Date(inv.created_at).toLocaleDateString()}
                      </span>
                      <button
                        onClick={() => handleDeleteInvitation(inv.id)}
                        className="btn-secondary"
                        title={t('users.invitations.delete')}
                        style={{ padding: '4px 8px', color: 'var(--danger)', borderColor: 'var(--danger)' }}
                      >
                        <Trash2 size={12} />
                      </button>
                    </div>
                  ))}
                </div>
              )}
            </div>
          </div>

          {/* Invitation links */}
          <div className="card">
            <div className="card-header" style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
              <Link2 size={14} />
              {t('users.invitations.linksTitle')}
            </div>
            <div style={{ padding: '16px' }}>
              <p style={{ fontSize: '0.75rem', color: 'var(--fg-muted)', marginBottom: '12px', lineHeight: 1.5 }}>
                {t('users.invitations.linksHint')}
              </p>
              <div style={{ display: 'flex', gap: '6px', marginBottom: '12px', alignItems: 'center' }}>
                <label style={{ fontSize: '0.75rem', color: 'var(--fg-muted)' }}>
                  {t('users.invitations.expiresInDays')}
                </label>
                <input
                  type="number"
                  min="0"
                  className="form-input"
                  value={linkExpiresDays}
                  onChange={e => setLinkExpiresDays(e.target.value)}
                  style={{ width: '70px' }}
                />
                <div style={{ flex: 1 }} />
                <button
                  onClick={handleCreateInviteLink}
                  disabled={linkSaving}
                  className="btn-primary"
                  style={{ padding: '6px 12px', fontSize: '0.8125rem' }}
                >
                  {linkSaving ? <Loader size={12} style={{ animation: 'spin 1s linear infinite' }} /> : <Plus size={12} />}
                  {t('users.invitations.generateLink')}
                </button>
              </div>
              {freshLinkToken && (
                <div style={{ marginBottom: '12px', padding: '10px 12px', borderRadius: '6px', background: 'var(--bg-base)', border: '1px solid var(--accent)' }}>
                  <div style={{ fontSize: '0.7rem', color: 'var(--fg-muted)', marginBottom: '4px', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
                    {t('users.invitations.freshLinkLabel')}
                  </div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                    <code style={{ flex: 1, fontFamily: MONO, fontSize: '0.75rem', color: 'var(--fg-primary)', wordBreak: 'break-all' }}>
                      {buildInviteUrl(freshLinkToken.token)}
                    </code>
                    <button
                      onClick={() => copyInviteUrl(freshLinkToken.id, freshLinkToken.token)}
                      className="btn-secondary"
                      style={{ padding: '4px 8px', fontSize: '0.75rem', flexShrink: 0 }}
                    >
                      {copiedLinkId === freshLinkToken.id ? <Check size={12} /> : <Copy size={12} />}
                    </button>
                  </div>
                  <div style={{ fontSize: '0.7rem', color: 'var(--warning)', marginTop: '6px' }}>
                    {t('users.invitations.freshLinkWarning')}
                  </div>
                </div>
              )}
              {inviteLinks.length === 0 ? (
                <div style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', padding: '12px 0', textAlign: 'center' }}>
                  {t('users.invitations.linksEmpty')}
                </div>
              ) : (
                <div style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
                  {inviteLinks.map(link => {
                    const isUsed = !!link.used_at
                    const isExpired = link.expires_at && new Date(link.expires_at) < new Date()
                    return (
                      <div
                        key={link.id}
                        className="flex items-center gap-3"
                        style={{ padding: '6px 10px', borderRadius: '6px', background: 'var(--bg-base)', opacity: isUsed || isExpired ? 0.6 : 1 }}
                      >
                        <div style={{ flex: 1, minWidth: 0 }}>
                          <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                            <span style={{ fontFamily: MONO, fontSize: '0.75rem', color: 'var(--fg-primary)' }}>
                              {link.id.slice(0, 8)}
                            </span>
                            {isUsed && (
                              <span className="badge badge-neutral">{t('users.invitations.linkUsed')}</span>
                            )}
                            {!isUsed && isExpired && (
                              <span className="badge badge-warning">{t('users.invitations.linkExpired')}</span>
                            )}
                          </div>
                          <div style={{ fontSize: '0.7rem', color: 'var(--fg-muted)', marginTop: '2px' }}>
                            {isUsed
                              ? t('users.invitations.usedBy', { email: link.used_by_email || '?', date: new Date(link.used_at!).toLocaleDateString() })
                              : link.expires_at
                                ? t('users.invitations.expiresAt', { date: new Date(link.expires_at).toLocaleDateString() })
                                : t('users.invitations.noExpiry')}
                          </div>
                        </div>
                        <button
                          onClick={() => handleDeleteInviteLink(link.id)}
                          className="btn-secondary"
                          title={t('users.invitations.delete')}
                          style={{ padding: '4px 8px', color: 'var(--danger)', borderColor: 'var(--danger)' }}
                        >
                          <Trash2 size={12} />
                        </button>
                      </div>
                    )
                  })}
                </div>
              )}
            </div>
          </div>
        </div>
      )}

      <div className="table-container">
        <table>
          <thead>
            <tr>
              {[t('users.columns.user'), t('users.columns.email'), t('users.columns.role'), ...(isRequestMode ? [t('users.columns.authorized')] : []), t('users.columns.joined'), ''].map(h => (
                <th key={h}>{h}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {users.map(u => (
              <tr key={u.id}>
                <td>
                  <div className="flex items-center gap-3">
                    {u.avatar_url && <img src={u.avatar_url} alt="" className="rounded-full" style={{ width: '28px', height: '28px' }} />}
                    <span style={{ fontWeight: 500 }}>{u.name}</span>
                  </div>
                </td>
                <td>
                  <span style={{ color: 'var(--fg-muted)' }}>{u.email}</span>
                </td>
                <td>
                  <span className={u.role === 'admin' ? 'badge badge-info' : 'badge badge-neutral'}>
                    {u.role}
                  </span>
                </td>
                {isRequestMode && (
                  <td>
                    {u.role === 'admin' ? (
                      <span className="badge badge-success">{t('users.authorized')}</span>
                    ) : u.authorized ? (
                      <span className="badge badge-success">{t('users.authorized')}</span>
                    ) : (
                      <span className="badge badge-neutral">{t('users.unauthorized')}</span>
                    )}
                  </td>
                )}
                <td>
                  <span style={{ color: 'var(--fg-muted)' }}>
                    {new Date(u.created_at).toLocaleDateString()}
                  </span>
                </td>
                <td>
                  {me?.role === 'admin' && me.id !== u.id && (
                    <button onClick={() => toggleRole(u)} className="btn-secondary" style={{ padding: '4px 12px', fontSize: '0.8125rem' }}>
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
