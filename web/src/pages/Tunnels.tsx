import { useEffect, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { api } from '../lib/api'
import type { ActiveTunnel, TunnelHistoryEntry, RuntimeConfig } from '../lib/types'
import { useTranslation } from 'react-i18next'

const MONO = 'var(--font-mono)'

function timeAgo(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime()
  const sec = Math.floor(ms / 1000)
  if (sec < 60) return `${sec}s`
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min}m`
  const hr = Math.floor(min / 60)
  if (hr < 24) return `${hr}h`
  return `${Math.floor(hr / 24)}d`
}

function duration(start: string, end: string | null): string {
  const s = new Date(start).getTime()
  const e = end ? new Date(end).getTime() : Date.now()
  const sec = Math.floor((e - s) / 1000)
  if (sec < 60) return `${sec}s`
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min}m ${sec % 60}s`
  const hr = Math.floor(min / 60)
  return `${hr}h ${min % 60}m`
}

export default function TunnelsPage() {
  const [active, setActive] = useState<ActiveTunnel[]>([])
  const [history, setHistory] = useState<TunnelHistoryEntry[]>([])
  const [config, setConfig] = useState<RuntimeConfig | null>(null)
  const [loading, setLoading] = useState(true)
  const { t } = useTranslation()

  const refresh = () => {
    setLoading(true)
    Promise.all([
      api.admin.tunnels(),
      api.admin.tunnelHistory(),
      api.runtime.config(),
    ]).then(([a, h, c]) => {
      setActive(a)
      setHistory(h)
      setConfig(c)
    }).catch(() => {}).finally(() => setLoading(false))
  }

  useEffect(refresh, [])

  const baseDomain = config?.base_domain || 'localhost'

  return (
    <div className="page-enter">
      <div className="page-header flex items-center justify-between">
        <div>
          <p className="page-subtitle">
            {t('tunnels.sectionLabel')}
          </p>
          <h1 className="page-title">
            {t('tunnels.heading')}
          </h1>
        </div>
        <button
          onClick={refresh}
          className="btn-secondary flex items-center gap-1.5"
        >
          <RefreshCw size={12} />
          {t('tunnels.refresh')}
        </button>
      </div>

      {/* Active Tunnels */}
      <h2 style={{ fontSize: '0.875rem', fontWeight: 600, color: 'var(--fg-primary)', marginBottom: '8px' }}>
        {t('tunnels.activeTitle')} ({active.length})
      </h2>
      <div className="table-container" style={{ marginBottom: '32px' }}>
        {active.map((tun, i) => (
          <div
            key={tun.domain}
            style={{
              background: 'var(--bg-card)',
              borderBottom: i < active.length - 1 ? '1px solid var(--border)' : 'none',
              padding: '0.85rem 1.25rem',
            }}
          >
            <div className="flex items-center gap-3">
              <div
                style={{ width: '8px', height: '8px', borderRadius: '50%', background: 'var(--success)', flexShrink: 0 }}
                className="status-running"
              />
              <div style={{ flex: 1 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                  <span style={{ fontSize: '0.875rem', fontWeight: 600, color: 'var(--fg-primary)' }}>
                    {tun.domain}.{baseDomain}
                  </span>
                  <span className={`badge ${tun.auth_required ? 'badge-info' : 'badge-success'}`}>
                    {tun.auth_required ? t('tunnels.auth') : t('tunnels.public')}
                  </span>
                  {tun.project_name ? (
                    <span className="badge badge-info">{t('tunnels.project')}: {tun.project_name}</span>
                  ) : (
                    <span className="badge badge-neutral">{t('tunnels.ephemeral')}</span>
                  )}
                </div>
                <div style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginTop: '2px' }}>
                  {tun.user_email} · {t('tunnels.upFor')} {duration(tun.connected_at, null)}
                </div>
              </div>
            </div>
          </div>
        ))}
        {active.length === 0 && !loading && (
          <div
            className="py-10 text-center"
            style={{ color: 'var(--fg-muted)', fontSize: '0.875rem', background: 'var(--bg-card)' }}
          >
            {t('tunnels.noActive')}
          </div>
        )}
      </div>

      {/* History */}
      <h2 style={{ fontSize: '0.875rem', fontWeight: 600, color: 'var(--fg-primary)', marginBottom: '8px' }}>
        {t('tunnels.historyTitle')}
      </h2>
      <div className="table-container">
        {/* Header */}
        <div
          style={{
            display: 'grid',
            gridTemplateColumns: '2fr 2fr 80px 100px 80px',
            gap: '8px',
            padding: '10px 1.25rem',
            borderBottom: '1px solid var(--border)',
            fontSize: '0.75rem',
            color: 'var(--fg-muted)',
            letterSpacing: '0.04em',
            fontWeight: 600,
            textTransform: 'uppercase',
          }}
        >
          <span>{t('tunnels.colDomain')}</span>
          <span>{t('tunnels.colUser')}</span>
          <span>{t('tunnels.colAuth')}</span>
          <span>{t('tunnels.colDuration')}</span>
          <span>{t('tunnels.colWhen')}</span>
        </div>
        {history.map((h, i) => {
          const isActive = !h.disconnected_at
          return (
            <div
              key={h.id}
              style={{
                display: 'grid',
                gridTemplateColumns: '2fr 2fr 80px 100px 80px',
                gap: '8px',
                padding: '10px 1.25rem',
                background: 'var(--bg-card)',
                borderBottom: i < history.length - 1 ? '1px solid var(--border)' : 'none',
                fontSize: '0.875rem',
                color: 'var(--fg-primary)',
              }}
            >
              <span style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                {isActive && (
                  <span
                    style={{ width: '6px', height: '6px', borderRadius: '50%', background: 'var(--success)', flexShrink: 0 }}
                    className="status-running"
                  />
                )}
                <span style={{ fontFamily: MONO }}>{h.domain}</span>
              </span>
              <span style={{ color: 'var(--fg-muted)' }}>{h.user_email}</span>
              <span>
                <span className={`badge ${h.auth_required ? 'badge-info' : 'badge-success'}`}>
                  {h.auth_required ? t('tunnels.auth') : t('tunnels.public')}
                </span>
              </span>
              <span style={{ color: 'var(--fg-muted)', fontFamily: MONO }}>
                {duration(h.connected_at, h.disconnected_at)}
              </span>
              <span style={{ color: 'var(--fg-muted)' }}>
                {timeAgo(h.connected_at)} {t('tunnels.ago')}
              </span>
            </div>
          )
        })}
        {history.length === 0 && !loading && (
          <div
            className="py-10 text-center"
            style={{ color: 'var(--fg-muted)', fontSize: '0.875rem', background: 'var(--bg-card)' }}
          >
            {t('tunnels.noHistory')}
          </div>
        )}
      </div>
    </div>
  )
}
