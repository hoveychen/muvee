import { useEffect, useState, useRef } from 'react'
import { ExternalLink, Lock, Globe, LayoutDashboard, Sun, Moon, Languages, Bot, Copy, Check } from 'lucide-react'
import type { PublicProject } from './lib/types'
import { useTranslation } from 'react-i18next'
import { useTheme } from './lib/theme'

function getApiBase(): string {
  return window.MUVEE_API_BASE ?? ''
}

function getDashboardUrl(): string {
  return window.MUVEE_DASHBOARD_URL || `${getApiBase()}/projects`
}

function fetchPublicProjects(): Promise<PublicProject[]> {
  return fetch(`${getApiBase()}/api/public/projects`)
    .then(r => r.ok ? r.json() : Promise.reject(new Error(r.statusText)))
    .then(data => Array.isArray(data) ? data : [])
}

function epochAgo(epochSeconds: number): string {
  const diff = Math.floor(Date.now() / 1000 - epochSeconds)
  if (diff < 60) return `${diff}s ago`
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`
  return `${Math.floor(diff / 86400)}d ago`
}

const MONO = 'var(--font-mono)'

export default function Community() {
  const [projects, setProjects] = useState<PublicProject[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    fetchPublicProjects()
      .then(setProjects)
      .catch(() => setProjects([]))
      .finally(() => setLoading(false))
  }, [])

  return (
    <div style={{ minHeight: '100vh', background: 'var(--bg-base)', color: 'var(--fg-primary)' }}>
      <style>{`
        @keyframes shimmer {
          0% { background-position: -200% center; }
          100% { background-position: 200% center; }
        }
        @keyframes fade-up {
          from { opacity: 0; transform: translateY(16px); }
          to   { opacity: 1; transform: translateY(0); }
        }
        .community-hero-inner { animation: fade-up 0.5s ease both; }
        .community-card { transition: border-color 0.18s, box-shadow 0.18s, transform 0.18s; }
        .community-card:hover { transform: translateY(-3px); }
        .community-card:hover .card-img { transform: scale(1.03); }
      `}</style>

      <Header />
      <Hero projectCount={loading ? -1 : projects.length} />

      <main style={{ maxWidth: '1200px', margin: '0 auto', padding: '2rem 1.5rem 6rem' }}>
        {loading ? (
          <LoadingGrid />
        ) : projects.length === 0 ? (
          <EmptyState />
        ) : (
          <div style={{
            display: 'grid',
            gridTemplateColumns: 'repeat(auto-fill, minmax(320px, 1fr))',
            gap: '1.25rem',
          }}>
            {projects.map(p => <ProjectCard key={p.id} project={p} />)}
          </div>
        )}
      </main>
    </div>
  )
}

// ─── Header ──────────────────────────────────────────────────────────────────

function Header() {
  const { t, i18n } = useTranslation()
  const { theme, toggleTheme } = useTheme()

  const toggleLang = () => i18n.changeLanguage(i18n.language === 'zh' ? 'en' : 'zh')

  const iconBtnStyle: React.CSSProperties = {
    background: 'none',
    border: '1px solid var(--border)',
    borderRadius: '6px',
    cursor: 'pointer',
    color: 'var(--fg-muted)',
    padding: '5px 7px',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    transition: 'color 0.15s, border-color 0.15s',
  }

  return (
    <header style={{
      borderBottom: '1px solid var(--border)',
      background: 'var(--bg-card)',
      position: 'sticky',
      top: 0,
      zIndex: 50,
    }}>
      <div style={{
        maxWidth: '1200px',
        margin: '0 auto',
        padding: '0 1.5rem',
        height: '52px',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: '12px',
      }}>
        {/* Logo */}
        <div className="flex items-center gap-2" style={{ flexShrink: 0 }}>
          <img src={`${getApiBase()}/favicon.png`} alt="Muvee" style={{ width: '24px', height: '24px', objectFit: 'contain' }} />
          <span style={{ fontWeight: 800, fontSize: '1rem', letterSpacing: '-0.02em', color: 'var(--fg-primary)' }}>
            Muvee
          </span>
        </div>

        {/* Right controls */}
        <div className="flex items-center gap-2">
          {/* Language toggle */}
          <button
            onClick={toggleLang}
            title={i18n.language === 'zh' ? t('nav.switchToEn') : t('nav.switchToZh')}
            style={iconBtnStyle}
            onMouseEnter={e => {
              const el = e.currentTarget as HTMLButtonElement
              el.style.color = 'var(--fg-primary)'
              el.style.borderColor = 'var(--fg-muted)'
            }}
            onMouseLeave={e => {
              const el = e.currentTarget as HTMLButtonElement
              el.style.color = 'var(--fg-muted)'
              el.style.borderColor = 'var(--border)'
            }}
          >
            <Languages size={14} />
          </button>

          {/* Theme toggle */}
          <button
            onClick={toggleTheme}
            title={theme === 'dark' ? t('nav.switchToLight') : t('nav.switchToDark')}
            style={iconBtnStyle}
            onMouseEnter={e => {
              const el = e.currentTarget as HTMLButtonElement
              el.style.color = 'var(--fg-primary)'
              el.style.borderColor = 'var(--fg-muted)'
            }}
            onMouseLeave={e => {
              const el = e.currentTarget as HTMLButtonElement
              el.style.color = 'var(--fg-muted)'
              el.style.borderColor = 'var(--border)'
            }}
          >
            {theme === 'dark' ? <Sun size={14} /> : <Moon size={14} />}
          </button>

          {/* Dashboard link */}
          <a
            href={getDashboardUrl()}
            className="flex items-center gap-1.5"
            style={{
              fontFamily: MONO,
              fontSize: '0.75rem',
              background: 'var(--bg-hover)',
              color: 'var(--fg-muted)',
              border: '1px solid var(--border)',
              borderRadius: '6px',
              padding: '5px 10px',
              textDecoration: 'none',
              fontWeight: 500,
              transition: 'color 0.15s, border-color 0.15s',
            }}
            onMouseEnter={e => {
              const el = e.currentTarget as HTMLAnchorElement
              el.style.color = 'var(--fg-primary)'
              el.style.borderColor = 'var(--fg-muted)'
            }}
            onMouseLeave={e => {
              const el = e.currentTarget as HTMLAnchorElement
              el.style.color = 'var(--fg-muted)'
              el.style.borderColor = 'var(--border)'
            }}
          >
            <LayoutDashboard size={13} />
            {t('community.enterDashboard')}
          </a>
        </div>
      </div>
    </header>
  )
}

// ─── Hero ─────────────────────────────────────────────────────────────────────

function SkillCopyBox() {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const skillUrl = `${getApiBase()}/api/skill`

  const handleCopy = () => {
    navigator.clipboard.writeText(skillUrl).then(() => {
      setCopied(true)
      if (timerRef.current) clearTimeout(timerRef.current)
      timerRef.current = setTimeout(() => setCopied(false), 2000)
    })
  }

  return (
    <div style={{
      display: 'inline-flex',
      flexDirection: 'column',
      alignItems: 'stretch',
      gap: '8px',
      marginTop: '1.75rem',
      maxWidth: '520px',
      width: '100%',
      textAlign: 'left',
    }}>
      <div style={{
        display: 'flex',
        alignItems: 'center',
        gap: '6px',
        fontSize: '0.72rem',
        fontFamily: MONO,
        color: 'var(--fg-muted)',
        letterSpacing: '0.06em',
        textTransform: 'uppercase',
      }}>
        <Bot size={12} style={{ color: 'var(--accent)' }} />
        {t('community.skillLabel')}
      </div>
      <div style={{
        display: 'flex',
        alignItems: 'center',
        background: 'var(--bg-card)',
        border: '1px solid var(--border)',
        borderRadius: '8px',
        overflow: 'hidden',
      }}>
        <span style={{
          flex: 1,
          fontFamily: MONO,
          fontSize: '0.78rem',
          color: 'var(--accent)',
          padding: '0.55rem 0.85rem',
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
        }}>
          {skillUrl}
        </span>
        <button
          onClick={handleCopy}
          title={copied ? t('community.skillCopied') : t('community.skillCopy')}
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: '5px',
            padding: '0.55rem 0.85rem',
            background: copied ? 'rgba(63,185,80,0.1)' : 'var(--bg-hover)',
            border: 'none',
            borderLeft: '1px solid var(--border)',
            cursor: 'pointer',
            color: copied ? '#3fb950' : 'var(--fg-muted)',
            fontFamily: MONO,
            fontSize: '0.72rem',
            fontWeight: 600,
            transition: 'color 0.15s, background 0.15s',
            flexShrink: 0,
            whiteSpace: 'nowrap',
          }}
        >
          {copied ? <Check size={12} /> : <Copy size={12} />}
          {copied ? t('community.skillCopied') : t('community.skillCopy')}
        </button>
      </div>
      <p style={{
        margin: 0,
        fontSize: '0.75rem',
        color: 'var(--fg-muted)',
        lineHeight: 1.6,
        fontFamily: MONO,
      }}>
        {t('community.skillDesc')}
      </p>
    </div>
  )
}

function Hero({ projectCount }: { projectCount: number }) {
  const { t } = useTranslation()
  return (
    <div style={{
      position: 'relative',
      overflow: 'hidden',
      borderBottom: '1px solid var(--border)',
    }}>
      {/* Dot-grid background */}
      <div style={{
        position: 'absolute',
        inset: 0,
        backgroundImage: 'radial-gradient(circle, var(--border) 1px, transparent 1px)',
        backgroundSize: '28px 28px',
        opacity: 0.6,
        pointerEvents: 'none',
      }} />
      {/* Radial fade to hide edges */}
      <div style={{
        position: 'absolute',
        inset: 0,
        background: 'radial-gradient(ellipse 70% 100% at 50% 50%, transparent 40%, var(--bg-base) 100%)',
        pointerEvents: 'none',
      }} />

      <div
        className="community-hero-inner"
        style={{
          position: 'relative',
          maxWidth: '1200px',
          margin: '0 auto',
          padding: '4.5rem 1.5rem 3.5rem',
          textAlign: 'center',
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
        }}
      >
        {/* Platform chip */}
        <div style={{
          display: 'inline-flex',
          alignItems: 'center',
          gap: '6px',
          padding: '4px 12px',
          background: 'var(--bg-card)',
          border: '1px solid var(--border)',
          borderRadius: '20px',
          fontFamily: MONO,
          fontSize: '0.7rem',
          color: 'var(--fg-muted)',
          marginBottom: '1.5rem',
        }}>
          <img src={`${getApiBase()}/favicon.png`} alt="" style={{ width: '14px', height: '14px', objectFit: 'contain' }} />
          Muvee · {t('community.platformLabel')}
        </div>

        <h1 style={{
          fontSize: 'clamp(2rem, 5vw, 3rem)',
          fontWeight: 900,
          lineHeight: 1.1,
          letterSpacing: '-0.04em',
          margin: '0 0 1rem',
          color: 'var(--fg-primary)',
        }}>
          {t('community.heroTitle')}
        </h1>

        <p style={{
          fontSize: '1rem',
          color: 'var(--fg-muted)',
          maxWidth: '460px',
          margin: '0 auto 0',
          lineHeight: 1.7,
        }}>
          {t('community.heroDesc')}
        </p>

        <SkillCopyBox />

        {projectCount > 0 && (
          <div style={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: '7px',
            padding: '5px 14px',
            background: 'rgba(63,185,80,0.08)',
            border: '1px solid rgba(63,185,80,0.22)',
            borderRadius: '20px',
            fontFamily: MONO,
            fontSize: '0.75rem',
            color: '#3fb950',
            marginTop: '1.25rem',
          }}>
            <span style={{
              width: '6px', height: '6px',
              borderRadius: '50%',
              background: '#3fb950',
              boxShadow: '0 0 6px #3fb950',
              flexShrink: 0,
            }} />
            {t('community.runningCount', { count: projectCount })}
          </div>
        )}
      </div>
    </div>
  )
}

// ─── Project Card ─────────────────────────────────────────────────────────────

function ProjectCard({ project }: { project: PublicProject }) {
  const [screenshotError, setScreenshotError] = useState(false)
  const [showAuthModal, setShowAuthModal] = useState(false)
  const { t } = useTranslation()

  const screenshotURL = project.auth_required
    ? null
    : `https://image.thum.io/get/width/640/crop/400/noanimate/${project.url}`

  const handleClick = (e: React.MouseEvent) => {
    if (project.auth_required) {
      e.preventDefault()
      setShowAuthModal(true)
    }
  }

  return (
    <>
      <a
        href={project.url}
        target="_blank"
        rel="noopener noreferrer"
        onClick={handleClick}
        className="community-card"
        style={{
          display: 'block',
          background: 'var(--bg-card)',
          border: '1px solid var(--border)',
          borderRadius: '12px',
          overflow: 'hidden',
          textDecoration: 'none',
          color: 'inherit',
          cursor: 'pointer',
        }}
        onMouseEnter={e => {
          const el = e.currentTarget as HTMLAnchorElement
          el.style.borderColor = 'var(--accent)'
          el.style.boxShadow = '0 6px 24px rgba(88,166,255,0.12)'
        }}
        onMouseLeave={e => {
          const el = e.currentTarget as HTMLAnchorElement
          el.style.borderColor = 'var(--border)'
          el.style.boxShadow = 'none'
        }}
      >
        {/* Screenshot */}
        <div style={{
          width: '100%',
          aspectRatio: '16/10',
          background: 'var(--bg-hover)',
          position: 'relative',
          overflow: 'hidden',
          borderBottom: '1px solid var(--border)',
        }}>
          {project.auth_required || screenshotError ? (
            <ScreenshotPlaceholder authRequired={project.auth_required} />
          ) : (
            <img
              src={screenshotURL!}
              alt={project.name}
              className="card-img"
              onError={() => setScreenshotError(true)}
              style={{
                width: '100%', height: '100%',
                objectFit: 'cover', objectPosition: 'top',
                display: 'block',
                transition: 'transform 0.3s ease',
              }}
            />
          )}
          {project.auth_required && (
            <div style={{
              position: 'absolute', top: '10px', right: '10px',
              display: 'flex', alignItems: 'center', gap: '4px',
              padding: '3px 9px',
              background: 'rgba(0,0,0,0.72)',
              backdropFilter: 'blur(6px)',
              borderRadius: '20px',
              fontSize: '0.65rem', fontFamily: MONO,
              color: '#e3b341',
              border: '1px solid rgba(227,179,65,0.3)',
            }}>
              <Lock size={9} />
              {t('community.loginRequired')}
            </div>
          )}
        </div>

        {/* Info */}
        <div style={{ padding: '1rem 1.15rem' }}>
          <div className="flex items-center justify-between gap-2">
            <h3 style={{
              fontSize: '0.95rem', fontWeight: 700, margin: 0,
              overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
            }}>
              {project.name}
            </h3>
            <ExternalLink size={13} style={{ color: 'var(--fg-muted)', flexShrink: 0, opacity: 0.5 }} />
          </div>

          <div className="flex items-center gap-1.5 mt-1.5">
            <Globe size={11} style={{ color: 'var(--fg-muted)', flexShrink: 0 }} />
            <span style={{
              fontFamily: MONO, fontSize: '0.71rem', color: 'var(--accent)',
              overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
            }}>
              {project.url.replace('https://', '')}
            </span>
          </div>

          <div className="flex items-center justify-between mt-2.5 pt-2.5"
            style={{ borderTop: '1px solid var(--border)' }}>
            <AuthorChip name={project.owner_name} avatarUrl={project.owner_avatar_url} />
            <span style={{ fontFamily: MONO, fontSize: '0.67rem', color: 'var(--fg-muted)', flexShrink: 0 }}>
              {epochAgo(project.updated_at)}
            </span>
          </div>
        </div>
      </a>

      {showAuthModal && (
        <AuthModal project={project} onClose={() => setShowAuthModal(false)} />
      )}
    </>
  )
}

function AuthorChip({ name, avatarUrl }: { name: string; avatarUrl: string }) {
  const [imgError, setImgError] = useState(false)
  return (
    <div className="flex items-center gap-1.5" style={{ minWidth: 0 }}>
      {avatarUrl && !imgError ? (
        <img
          src={avatarUrl} alt={name}
          onError={() => setImgError(true)}
          style={{ width: '18px', height: '18px', borderRadius: '50%', objectFit: 'cover', border: '1px solid var(--border)', flexShrink: 0 }}
        />
      ) : (
        <div style={{
          width: '18px', height: '18px', borderRadius: '50%',
          background: 'var(--accent)',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          fontSize: '0.58rem', color: '#fff', fontWeight: 700, flexShrink: 0,
          opacity: 0.8,
        }}>
          {name.charAt(0).toUpperCase()}
        </div>
      )}
      <span style={{
        fontSize: '0.78rem', color: 'var(--fg-muted)',
        overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
      }}>
        {name}
      </span>
    </div>
  )
}

function ScreenshotPlaceholder({ authRequired }: { authRequired: boolean }) {
  const { t } = useTranslation()
  return (
    <div style={{
      width: '100%', height: '100%',
      display: 'flex', flexDirection: 'column',
      alignItems: 'center', justifyContent: 'center', gap: '10px',
    }}>
      {authRequired ? (
        <>
          <div style={{
            width: '40px', height: '40px', borderRadius: '50%',
            background: 'rgba(227,179,65,0.08)',
            border: '1px solid rgba(227,179,65,0.2)',
            display: 'flex', alignItems: 'center', justifyContent: 'center',
          }}>
            <Lock size={16} style={{ color: '#e3b341', opacity: 0.65 }} />
          </div>
          <span style={{ fontFamily: MONO, fontSize: '0.7rem', color: 'var(--fg-muted)', opacity: 0.65 }}>
            {t('community.loginRequired')}
          </span>
        </>
      ) : (
        <>
          <Globe size={22} style={{ color: 'var(--fg-muted)', opacity: 0.25 }} />
          <span style={{ fontFamily: MONO, fontSize: '0.7rem', color: 'var(--fg-muted)', opacity: 0.4 }}>
            {t('community.noPreview')}
          </span>
        </>
      )}
    </div>
  )
}

// ─── Auth Modal ───────────────────────────────────────────────────────────────

function AuthModal({ project, onClose }: { project: PublicProject; onClose: () => void }) {
  const { t } = useTranslation()
  return (
    <div
      style={{
        position: 'fixed', inset: 0,
        background: 'rgba(0,0,0,0.6)',
        backdropFilter: 'blur(6px)',
        zIndex: 100,
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        padding: '1rem',
      }}
      onClick={onClose}
    >
      <div
        style={{
          background: 'var(--bg-card)',
          border: '1px solid var(--border)',
          borderRadius: '14px',
          padding: '1.75rem',
          maxWidth: '380px', width: '100%',
          boxShadow: '0 24px 80px rgba(0,0,0,0.5)',
        }}
        onClick={e => e.stopPropagation()}
      >
        <div className="flex items-center gap-3 mb-4">
          <div style={{
            width: '38px', height: '38px', borderRadius: '9px',
            background: 'rgba(227,179,65,0.08)',
            border: '1px solid rgba(227,179,65,0.22)',
            display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0,
          }}>
            <Lock size={16} style={{ color: '#e3b341' }} />
          </div>
          <div style={{ minWidth: 0 }}>
            <p style={{ fontFamily: MONO, fontSize: '0.62rem', color: 'var(--fg-muted)', letterSpacing: '0.08em', margin: 0 }}>
              {t('community.authModal.label')}
            </p>
            <h3 style={{
              fontSize: '0.95rem', fontWeight: 700, margin: 0,
              overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
            }}>
              {project.name}
            </h3>
          </div>
        </div>
        <p style={{ fontSize: '0.85rem', color: 'var(--fg-muted)', lineHeight: 1.65, marginBottom: '1.25rem' }}>
          {t('community.authModal.desc')}
        </p>
        <div className="flex gap-2">
          <a
            href={project.url}
            target="_blank"
            rel="noopener noreferrer"
            style={{
              flex: 1, padding: '0.6rem 1rem',
              background: 'var(--accent)', color: '#fff',
              borderRadius: '7px', fontSize: '0.85rem', fontWeight: 600,
              textAlign: 'center', textDecoration: 'none',
            }}
          >
            {t('community.authModal.proceed')}
          </a>
          <button
            onClick={onClose}
            style={{
              padding: '0.6rem 1rem',
              background: 'var(--bg-hover)', color: 'var(--fg-muted)',
              border: '1px solid var(--border)',
              borderRadius: '7px', fontSize: '0.85rem', cursor: 'pointer',
            }}
          >
            {t('community.authModal.cancel')}
          </button>
        </div>
      </div>
    </div>
  )
}

// ─── Loading / Empty ──────────────────────────────────────────────────────────

function LoadingGrid() {
  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(320px, 1fr))', gap: '1.25rem' }}>
      {Array.from({ length: 6 }).map((_, i) => (
        <div key={i} style={{ background: 'var(--bg-card)', border: '1px solid var(--border)', borderRadius: '12px', overflow: 'hidden' }}>
          <div style={{
            width: '100%', aspectRatio: '16/10',
            background: `linear-gradient(90deg, var(--bg-hover) 25%, var(--bg-card) 50%, var(--bg-hover) 75%)`,
            backgroundSize: '200% 100%',
            animation: `shimmer 1.5s infinite`,
            animationDelay: `${i * 0.1}s`,
          }} />
          <div style={{ padding: '1rem 1.15rem' }}>
            <div style={{ height: '13px', background: 'var(--bg-hover)', borderRadius: '4px', marginBottom: '10px', width: '55%', opacity: 0.6 }} />
            <div style={{ height: '11px', background: 'var(--bg-hover)', borderRadius: '4px', width: '75%', opacity: 0.4 }} />
          </div>
        </div>
      ))}
    </div>
  )
}

function EmptyState() {
  const { t } = useTranslation()
  return (
    <div style={{ textAlign: 'center', padding: '6rem 1rem', color: 'var(--fg-muted)' }}>
      <div style={{
        width: '60px', height: '60px', borderRadius: '16px',
        background: 'var(--bg-card)', border: '1px solid var(--border)',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        margin: '0 auto 1.25rem',
      }}>
        <Globe size={24} style={{ opacity: 0.35 }} />
      </div>
      <p style={{ fontSize: '1rem', fontWeight: 700, color: 'var(--fg-primary)', marginBottom: '0.5rem' }}>
        {t('community.emptyTitle')}
      </p>
      <p style={{ fontFamily: MONO, fontSize: '0.8rem' }}>
        {t('community.emptyDesc')}
      </p>
    </div>
  )
}
