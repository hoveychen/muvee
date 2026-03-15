import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'

const MONO = 'var(--font-mono)'

export default function LoginPage() {
  const { t } = useTranslation()

  useEffect(() => {
    document.title = 'muvee — Sign In'
  }, [])

  return (
    <div className="min-h-screen flex" style={{ background: 'var(--bg-base)' }}>
      {/* Left panel */}
      <div
        className="hidden lg:flex flex-col justify-between w-1/2 p-16 relative overflow-hidden"
        style={{ background: 'var(--bg-card)', borderRight: '1px solid var(--border)' }}
      >
        {/* Subtle grid pattern */}
        <div
          className="absolute inset-0 pointer-events-none"
          style={{
            backgroundImage: `radial-gradient(circle at 1px 1px, var(--border) 1px, transparent 0)`,
            backgroundSize: '28px 28px',
            opacity: 0.4,
          }}
        />

        <div className="relative z-10">
          <span
            className="text-xs tracking-widest uppercase"
            style={{ color: 'var(--fg-muted)', fontFamily: MONO }}
          >
            {t('login.tagline')}
          </span>
        </div>

        <div className="relative z-10">
          <h1
            className="font-bold leading-none tracking-tight"
            style={{ fontSize: '6rem', color: 'var(--fg-primary)', lineHeight: '1' }}
          >
            muvee
          </h1>
          <p
            className="mt-6 text-lg max-w-sm"
            style={{ color: 'var(--fg-muted)', lineHeight: '1.7', whiteSpace: 'pre-line' }}
          >
            {t('login.description')}
          </p>
        </div>

        <div className="relative z-10 flex gap-6" style={{ color: 'var(--fg-muted)', fontFamily: MONO, fontSize: '0.78rem' }}>
          <span>{t('login.feat1')}</span>
          <span style={{ color: 'var(--border)' }}>·</span>
          <span>{t('login.feat2')}</span>
          <span style={{ color: 'var(--border)' }}>·</span>
          <span>{t('login.feat3')}</span>
        </div>
      </div>

      {/* Right panel */}
      <div className="flex-1 flex flex-col items-center justify-center p-8 lg:p-16">
        <div
          className="w-full max-w-sm page-enter"
          style={{ animation: 'page-enter 300ms ease-out' }}
        >
          {/* Mobile logo */}
          <div className="lg:hidden mb-10">
            <h1
              className="text-5xl font-bold"
              style={{ color: 'var(--fg-primary)' }}
            >
              muvee
            </h1>
          </div>

          <div
            className="rounded-md p-8"
            style={{ background: 'var(--bg-card)', border: '1px solid var(--border)' }}
          >
            <h2
              className="text-xl font-semibold mb-1"
              style={{ color: 'var(--fg-primary)' }}
            >
              {t('login.signIn')}
            </h2>
            <p
              className="text-sm mb-8"
              style={{ fontFamily: MONO, color: 'var(--fg-muted)' }}
            >
              {t('login.authorizedOnly')}
            </p>

            <a
              href="/auth/google/login"
              className="flex items-center justify-center gap-3 w-full py-2.5 px-4 rounded-md text-sm transition-all duration-150"
              style={{
                background: 'var(--bg-hover)',
                color: 'var(--fg-primary)',
                border: '1px solid var(--border)',
                fontWeight: 500,
                textDecoration: 'none',
              }}
              onMouseEnter={e => {
                (e.currentTarget as HTMLAnchorElement).style.borderColor = 'var(--accent)'
              }}
              onMouseLeave={e => {
                (e.currentTarget as HTMLAnchorElement).style.borderColor = 'var(--border)'
              }}
            >
              <GoogleIcon />
              {t('login.continueWithGoogle')}
            </a>
          </div>

          <p
            className="text-center mt-5 text-xs"
            style={{ fontFamily: MONO, color: 'var(--fg-muted)' }}
          >
            {t('login.accessRestricted')}
          </p>
        </div>
      </div>
    </div>
  )
}

function GoogleIcon() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24">
      <path d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z" fill="#4285F4"/>
      <path d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z" fill="#34A853"/>
      <path d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z" fill="#FBBC05"/>
      <path d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z" fill="#EA4335"/>
    </svg>
  )
}
