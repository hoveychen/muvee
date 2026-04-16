import { useEffect, useMemo, useState } from 'react'
import { Lock, Inbox, Search } from 'lucide-react'
import { useSettings } from '../lib/settings'
import { useTranslation } from 'react-i18next'

interface PublicProject {
  id: string
  name: string
  domain_prefix: string
  description: string
  icon: string
  tags: string
  url: string
  auth_required: boolean
  owner_name: string
  owner_avatar_url: string
  updated_at: number
}

export default function PortalPage() {
  const [projects, setProjects] = useState<PublicProject[]>([])
  const [loading, setLoading] = useState(true)
  const [search, setSearch] = useState('')
  const [activeTag, setActiveTag] = useState('')
  const { t } = useTranslation()
  const { settings } = useSettings()

  useEffect(() => {
    fetch('/api/public/projects', { credentials: 'include' })
      .then(r => r.ok ? r.json() : [])
      .then(setProjects)
      .finally(() => setLoading(false))
  }, [])

  // Collect all unique tags
  const allTags = useMemo(() => {
    const set = new Set<string>()
    projects.forEach(p => {
      if (p.tags) p.tags.split(',').forEach(t => { const s = t.trim(); if (s) set.add(s) })
    })
    return Array.from(set).sort()
  }, [projects])

  // Filter projects by search + tag
  const filtered = useMemo(() => {
    const q = search.toLowerCase().trim()
    return projects.filter(p => {
      if (activeTag) {
        const tags = p.tags.split(',').map(t => t.trim())
        if (!tags.includes(activeTag)) return false
      }
      if (!q) return true
      return (
        p.name.toLowerCase().includes(q) ||
        p.description.toLowerCase().includes(q) ||
        p.tags.toLowerCase().includes(q) ||
        p.domain_prefix.toLowerCase().includes(q)
      )
    })
  }, [projects, search, activeTag])

  const siteName = settings.site_name || 'Muvee'

  return (
    <div className="page-enter">
      {/* Hero */}
      <div style={{ marginBottom: '24px' }}>
        <h1 style={{ fontSize: '1.75rem', fontWeight: 700, color: 'var(--fg-primary)', lineHeight: 1.3 }}>
          {t('portal.heading', { name: siteName })}
        </h1>
        <p style={{ fontSize: '0.875rem', color: 'var(--fg-muted)', marginTop: '6px', lineHeight: 1.6 }}>
          {t('portal.subtitle')}
        </p>
      </div>

      {/* Search + tag filter bar */}
      {!loading && projects.length > 0 && (
        <div style={{ marginBottom: '24px' }}>
          <div style={{
            display: 'flex', alignItems: 'center', gap: '8px',
            background: 'var(--bg-card)', border: '1px solid var(--border)',
            borderRadius: '10px', padding: '8px 14px',
          }}>
            <Search size={16} style={{ color: 'var(--fg-muted)', flexShrink: 0 }} />
            <input
              type="text"
              value={search}
              onChange={e => setSearch(e.target.value)}
              placeholder={t('portal.searchPlaceholder')}
              style={{
                flex: 1, background: 'none', border: 'none', outline: 'none',
                fontSize: '0.875rem', color: 'var(--fg-primary)',
              }}
            />
          </div>
          {allTags.length > 0 && (
            <div style={{ display: 'flex', gap: '6px', flexWrap: 'wrap', marginTop: '10px' }}>
              <TagButton label={t('portal.allTags')} active={!activeTag} onClick={() => setActiveTag('')} />
              {allTags.map(tag => (
                <TagButton key={tag} label={tag} active={activeTag === tag} onClick={() => setActiveTag(tag === activeTag ? '' : tag)} />
              ))}
            </div>
          )}
        </div>
      )}

      {loading ? (
        <div style={{ color: 'var(--fg-muted)', fontSize: '0.875rem' }}>{t('common.loading')}</div>
      ) : projects.length === 0 ? (
        <div style={{
          display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center',
          padding: '60px 20px', textAlign: 'center',
          border: '1px dashed var(--border)', borderRadius: '12px',
        }}>
          <Inbox size={40} style={{ color: 'var(--border)', marginBottom: '16px' }} />
          <p style={{ fontSize: '1rem', fontWeight: 600, color: 'var(--fg-muted)' }}>
            {t('portal.empty')}
          </p>
          <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginTop: '6px', maxWidth: '360px' }}>
            {t('portal.emptyHint')}
          </p>
        </div>
      ) : filtered.length === 0 ? (
        <div style={{ textAlign: 'center', padding: '40px 20px', color: 'var(--fg-muted)', fontSize: '0.875rem' }}>
          {t('portal.noResults')}
        </div>
      ) : (
        <div style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fill, minmax(160px, 1fr))',
          gap: '16px',
        }}>
          {filtered.map(p => (
            <AppCard key={p.id} project={p} />
          ))}
        </div>
      )}
    </div>
  )
}

function TagButton({ label, active, onClick }: { label: string; active: boolean; onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      style={{
        fontSize: '0.75rem',
        padding: '3px 10px',
        borderRadius: '99px',
        border: '1px solid',
        borderColor: active ? 'var(--accent)' : 'var(--border)',
        background: active ? 'rgba(37,99,235,0.08)' : 'transparent',
        color: active ? 'var(--accent)' : 'var(--fg-muted)',
        cursor: 'pointer',
        transition: 'all 150ms',
      }}
    >
      {label}
    </button>
  )
}

function AppIcon({ project }: { project: PublicProject }) {
  // Derive a stable hue from the project name
  let hash = 0
  for (let i = 0; i < project.name.length; i++) hash = (hash * 31 + project.name.charCodeAt(i)) | 0
  const hue = Math.abs(hash) % 360

  const size = 64
  const style: React.CSSProperties = {
    width: size, height: size, borderRadius: '16px', flexShrink: 0,
    display: 'flex', alignItems: 'center', justifyContent: 'center',
    overflow: 'hidden',
  }

  const icon = project.icon?.trim()

  // SVG content
  if (icon && icon.startsWith('<svg')) {
    return (
      <div style={style} dangerouslySetInnerHTML={{ __html: icon }} />
    )
  }

  // Image URL
  if (icon && (icon.startsWith('http://') || icon.startsWith('https://'))) {
    return (
      <div style={{ ...style, background: `hsl(${hue}, 30%, 94%)` }}>
        <img
          src={icon}
          alt={project.name}
          style={{ width: '100%', height: '100%', objectFit: 'cover', borderRadius: '16px' }}
          onError={e => { (e.target as HTMLImageElement).style.display = 'none' }}
        />
      </div>
    )
  }

  // Fallback: colored square with initial
  return (
    <div style={{
      ...style,
      background: `hsl(${hue}, 40%, 90%)`,
      color: `hsl(${hue}, 50%, 35%)`,
      fontSize: '1.5rem', fontWeight: 700,
    }}>
      {project.name.charAt(0).toUpperCase()}
    </div>
  )
}

function AppCard({ project }: { project: PublicProject }) {
  const { t } = useTranslation()
  const tags = project.tags ? project.tags.split(',').map(t => t.trim()).filter(Boolean) : []

  return (
    <a
      href={project.url}
      target="_blank"
      rel="noopener noreferrer"
      style={{
        display: 'flex', flexDirection: 'column', alignItems: 'center',
        textAlign: 'center',
        background: 'var(--bg-card)',
        border: '1px solid var(--border)',
        borderRadius: '12px',
        padding: '20px 12px 16px',
        textDecoration: 'none',
        transition: 'transform 150ms, box-shadow 150ms',
        cursor: 'pointer',
      }}
      onMouseEnter={e => {
        e.currentTarget.style.transform = 'translateY(-2px)'
        e.currentTarget.style.boxShadow = '0 4px 16px rgba(0,0,0,0.08)'
      }}
      onMouseLeave={e => {
        e.currentTarget.style.transform = 'none'
        e.currentTarget.style.boxShadow = 'none'
      }}
    >
      <AppIcon project={project} />

      {/* App name */}
      <div style={{
        fontSize: '0.875rem', fontWeight: 600, color: 'var(--fg-primary)',
        marginTop: '12px', lineHeight: 1.3,
        overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
        maxWidth: '100%',
      }}>
        {project.name}
      </div>

      {/* Description or domain */}
      <div style={{
        fontSize: '0.75rem', color: 'var(--fg-muted)', marginTop: '4px',
        lineHeight: 1.4,
        overflow: 'hidden', textOverflow: 'ellipsis',
        display: '-webkit-box', WebkitLineClamp: 2, WebkitBoxOrient: 'vertical',
        maxWidth: '100%',
      }}>
        {project.description || project.domain_prefix}
      </div>

      {/* Tags */}
      {tags.length > 0 && (
        <div style={{ display: 'flex', gap: '4px', flexWrap: 'wrap', justifyContent: 'center', marginTop: '8px' }}>
          {tags.slice(0, 3).map(tag => (
            <span key={tag} style={{
              fontSize: '0.625rem', padding: '1px 6px',
              borderRadius: '99px', background: 'var(--bg-hover)',
              color: 'var(--fg-muted)',
            }}>
              {tag}
            </span>
          ))}
        </div>
      )}

      {/* Footer: owner + auth badge */}
      <div style={{
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        gap: '6px', marginTop: '10px', width: '100%',
      }}>
        {project.owner_avatar_url ? (
          <img src={project.owner_avatar_url} alt="" style={{ width: '16px', height: '16px', borderRadius: '50%' }} />
        ) : (
          <div style={{
            width: '16px', height: '16px', borderRadius: '50%',
            background: 'var(--bg-hover)', display: 'flex', alignItems: 'center', justifyContent: 'center',
            fontSize: '0.55rem', fontWeight: 600, color: 'var(--fg-muted)',
          }}>
            {(project.owner_name || '?').charAt(0).toUpperCase()}
          </div>
        )}
        <span style={{ fontSize: '0.6875rem', color: 'var(--fg-muted)' }}>
          {project.owner_name}
        </span>
        {project.auth_required && (
          <Lock size={10} style={{ color: 'var(--fg-muted)', opacity: 0.6 }} />
        )}
      </div>
    </a>
  )
}
