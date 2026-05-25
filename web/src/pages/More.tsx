import { useEffect, useState } from 'react'
import { NavLink, useNavigate } from 'react-router-dom'
import {
  ChevronRight, User, Key, Sun, Moon, Languages, LogOut,
  Server, Globe, Users as UsersIcon, Settings as SettingsIcon,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../lib/auth'
import { useTheme } from '../lib/theme'
import { api } from '../lib/api'
import type { RuntimeConfig } from '../lib/types'

const MONO = 'var(--font-mono)'

type Row =
  | { kind: 'link'; to: string; icon: typeof User; label: string; trailing?: string }
  | { kind: 'action'; onClick: () => void; icon: typeof User; label: string; trailing?: string; danger?: boolean }

function SectionList({ title, rows }: { title: string; rows: Row[] }) {
  return (
    <div style={{ marginBottom: 18 }}>
      <div style={{ padding: '4px 4px 6px', fontSize: '0.6875rem', fontWeight: 600, color: 'var(--fg-muted)', textTransform: 'uppercase', letterSpacing: '0.06em' }}>
        {title}
      </div>
      <div className="card">
        {rows.map((row, i) => {
          const Icon = row.icon
          const last = i === rows.length - 1
          const inner = (
            <>
              <Icon size={18} style={{ flexShrink: 0, color: row.kind === 'action' && row.danger ? 'var(--danger)' : 'var(--fg-muted)' }} />
              <span style={{ flex: 1, fontSize: '0.9375rem', color: row.kind === 'action' && row.danger ? 'var(--danger)' : 'var(--fg-primary)', fontWeight: 500 }}>
                {row.label}
              </span>
              {row.trailing && (
                <span style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', fontFamily: row.trailing.length < 4 ? MONO : 'inherit' }}>
                  {row.trailing}
                </span>
              )}
              {row.kind === 'link' && <ChevronRight size={16} style={{ color: 'var(--fg-muted)', flexShrink: 0 }} />}
            </>
          )
          const baseStyle: React.CSSProperties = {
            display: 'flex',
            alignItems: 'center',
            gap: 12,
            padding: '14px 16px',
            borderBottom: last ? 'none' : '1px solid var(--border)',
            textDecoration: 'none',
            background: 'transparent',
            border: 'none',
            width: '100%',
            cursor: row.kind === 'action' ? 'pointer' : 'default',
            textAlign: 'left',
            fontFamily: 'inherit',
          }
          return row.kind === 'link' ? (
            <NavLink key={i} to={row.to} style={baseStyle}>{inner}</NavLink>
          ) : (
            <button key={i} onClick={row.onClick} style={baseStyle}>{inner}</button>
          )
        })}
      </div>
    </div>
  )
}

export default function MorePage() {
  const { t, i18n } = useTranslation()
  const { user } = useAuth()
  const { theme, toggleTheme } = useTheme()
  const navigate = useNavigate()
  const isAdmin = user?.role === 'admin'

  const [runtimeConfig, setRuntimeConfig] = useState<RuntimeConfig | null>(null)
  useEffect(() => { api.runtime.config().then(setRuntimeConfig).catch(() => {}) }, [])
  const secretsEnabled = runtimeConfig === null || runtimeConfig.secrets_enabled

  const handleLogout = async () => {
    if (!confirm(t('more.logoutConfirm', { name: user?.name || user?.email || '' }))) return
    await fetch('/auth/logout', { method: 'POST', credentials: 'include' })
    navigate('/login')
  }

  const toggleLang = () => {
    i18n.changeLanguage(i18n.language === 'zh' ? 'en' : 'zh')
  }

  // Account section — profile + tokens + (secrets when not on tab bar) + logout
  const accountRows: Row[] = [
    { kind: 'link', to: '/settings/profile', icon: User, label: t('nav.profile') },
    // Tokens appears here when it is NOT the 4th tab on the bottom bar
    ...(secretsEnabled ? [{ kind: 'link', to: '/settings/tokens', icon: Key, label: t('nav.tokens') } as Row] : []),
    { kind: 'action', onClick: handleLogout, icon: LogOut, label: t('nav.logout'), danger: true },
  ]

  const appearanceRows: Row[] = [
    {
      kind: 'action',
      onClick: toggleTheme,
      icon: theme === 'dark' ? Sun : Moon,
      label: theme === 'dark' ? t('more.themeLight') : t('more.themeDark'),
      trailing: theme === 'dark' ? '🌙' : '☀️',
    },
    {
      kind: 'action',
      onClick: toggleLang,
      icon: Languages,
      label: i18n.language === 'zh' ? t('more.langLabelZh') : t('more.langLabelEn'),
      trailing: i18n.language === 'zh' ? 'ZH' : 'EN',
    },
  ]

  const adminRows: Row[] = [
    { kind: 'link', to: '/nodes', icon: Server, label: t('nav.nodes') },
    { kind: 'link', to: '/tunnels', icon: Globe, label: t('nav.tunnels') },
    { kind: 'link', to: '/users', icon: UsersIcon, label: t('nav.users') },
    { kind: 'link', to: '/admin/settings', icon: SettingsIcon, label: t('nav.settings') },
  ]

  return (
    <div className="page-enter">
      <div className="page-header">
        <h1 className="page-title">{t('more.heading')}</h1>
        <p className="page-subtitle">{t('more.sectionLabel')}</p>
      </div>

      {user && (
        <NavLink
          to="/settings/profile"
          className="card"
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 14,
            padding: '14px 16px',
            textDecoration: 'none',
            marginBottom: 18,
          }}
        >
          {user.avatar_url ? (
            <img src={user.avatar_url} alt="" style={{ width: 48, height: 48, borderRadius: '50%' }} />
          ) : (
            <div style={{ width: 48, height: 48, borderRadius: '50%', background: 'var(--bg-hover)', display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: '1.125rem', color: 'var(--fg-primary)', fontWeight: 600 }}>
              {(user.name || user.email || '?').charAt(0).toUpperCase()}
            </div>
          )}
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ fontSize: '0.6875rem', color: 'var(--fg-muted)', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
              {t('more.signedInAs')}
            </div>
            <div style={{ fontSize: '1rem', fontWeight: 600, color: 'var(--fg-primary)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {user.name || user.email.split('@')[0]}
            </div>
            <div style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {user.email}
            </div>
          </div>
          <ChevronRight size={18} style={{ color: 'var(--fg-muted)', flexShrink: 0 }} />
        </NavLink>
      )}

      <SectionList title={t('nav.account')} rows={accountRows} />
      <SectionList title={t('nav.appearance')} rows={appearanceRows} />
      {isAdmin && <SectionList title={t('nav.administration')} rows={adminRows} />}
    </div>
  )
}
