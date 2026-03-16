import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '../lib/api'
import { useTranslation } from 'react-i18next'
import { resolveDatasetPath } from '../lib/utils'

const MONO = 'DM Mono'

const inputStyle: React.CSSProperties = {
  width: '100%',
  background: 'var(--bg-hover)',
  border: '1px solid var(--border)',
  color: 'var(--fg-primary)',
  fontFamily: MONO,
  fontSize: '0.85rem',
  padding: '0.5rem 0.75rem',
  borderRadius: '2px',
  outline: 'none',
}

const labelStyle: React.CSSProperties = {
  fontFamily: MONO,
  fontSize: '0.65rem',
  color: 'var(--fg-muted)',
  letterSpacing: '0.1em',
  display: 'block',
  marginBottom: '0.4rem',
}

function focusAccent(e: React.FocusEvent<HTMLInputElement>) {
  e.target.style.borderColor = 'var(--accent)'
}
function blurBorder(e: React.FocusEvent<HTMLInputElement>) {
  e.target.style.borderColor = 'var(--border)'
}

export default function NewDataset() {
  const navigate = useNavigate()
  const [name, setName] = useState('')
  const [nfsPath, setNfsPath] = useState('')
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [datasetBasePath, setDatasetBasePath] = useState('')
  const { t } = useTranslation()

  const canSubmit = name.trim() !== '' && nfsPath.trim() !== ''

  useEffect(() => {
    api.runtime.config()
      .then(cfg => setDatasetBasePath(cfg.dataset_nfs_base_path || ''))
      .catch(() => setDatasetBasePath(''))
  }, [])

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!canSubmit || submitting) return
    setError('')
    setSubmitting(true)
    try {
      const dataset = await api.datasets.create({ name: name.trim(), nfs_path: nfsPath.trim() })
      navigate('/datasets')
      void dataset
    } catch (err) {
      setError(err instanceof Error ? err.message : t('newDataset.failed'))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="page-enter" style={{ maxWidth: '560px' }}>
      <div style={{ marginBottom: '2rem' }}>
        <p style={{ fontFamily: MONO, color: 'var(--fg-muted)', fontSize: '0.72rem', letterSpacing: '0.05em' }}>{t('newDataset.sectionLabel')}</p>
        <h1 style={{ fontSize: '1.75rem', fontWeight: 700, color: 'var(--fg-primary)', lineHeight: 1.2, marginTop: '4px' }}>{t('newDataset.heading')}</h1>
      </div>

      <form onSubmit={handleSubmit}>
        <div style={{ border: '1px solid var(--border)', borderRadius: '6px', overflow: 'hidden', background: 'var(--bg-card)' }}>
          <div style={{ padding: '1.5rem', borderBottom: '1px solid var(--border)' }}>
            <div style={{ marginBottom: '1.25rem' }}>
              <label style={labelStyle}>{t('newDataset.name')}</label>
              <input
                style={inputStyle}
                value={name}
                onChange={e => setName(e.target.value)}
                onFocus={focusAccent}
                onBlur={blurBorder}
                placeholder="my-dataset"
                autoFocus
              />
            </div>

            <div>
              <label style={labelStyle}>{t('newDataset.nfsPath')}</label>
              <p style={{ fontFamily: MONO, fontSize: '0.7rem', color: 'var(--fg-muted)', marginBottom: '0.35rem' }}>
                {t('newDataset.datasetBasePath')}: <span style={{ color: 'var(--fg-primary)' }}>{datasetBasePath || t('newDataset.notConfigured')}</span>
              </p>
              <input
                style={inputStyle}
                value={nfsPath}
                onChange={e => setNfsPath(e.target.value)}
                onFocus={focusAccent}
                onBlur={blurBorder}
                placeholder="warehouse"
              />
              <p style={{ fontFamily: MONO, fontSize: '0.7rem', color: 'var(--fg-muted)', marginTop: '0.4rem' }}>
                {t('newDataset.nfsPathHint')}
              </p>
              {nfsPath.trim() !== '' && (
                <p style={{ fontFamily: MONO, fontSize: '0.7rem', color: 'var(--fg-muted)', marginTop: '0.3rem' }}>
                  {t('newDataset.fullPathPreview')}: <span style={{ color: 'var(--fg-primary)' }}>{resolveDatasetPath(datasetBasePath, nfsPath.trim())}</span>
                </p>
              )}
            </div>
          </div>

          {error && (
            <div style={{ padding: '0.75rem 1.5rem', background: 'var(--danger)18', borderBottom: '1px solid var(--border)' }}>
              <p style={{ fontFamily: MONO, fontSize: '0.78rem', color: 'var(--danger)' }}>{error}</p>
            </div>
          )}

          <div className="flex items-center justify-end gap-3" style={{ padding: '1rem 1.5rem' }}>
            <button
              type="button"
              onClick={() => navigate('/datasets')}
              style={{
                fontFamily: MONO, fontSize: '0.8rem', color: 'var(--fg-muted)',
                background: 'none', border: 'none', cursor: 'pointer', padding: '0.4rem 0.75rem',
              }}
            >
              {t('newDataset.cancel')}
            </button>
            <button
              type="submit"
              disabled={!canSubmit || submitting}
              style={{
                fontFamily: MONO, fontSize: '0.8rem', fontWeight: 600,
                background: canSubmit && !submitting ? 'var(--accent)' : 'var(--border)',
                color: canSubmit && !submitting ? '#ffffff' : 'var(--fg-muted)',
                border: 'none', borderRadius: '4px', cursor: canSubmit && !submitting ? 'pointer' : 'not-allowed',
                padding: '0.5rem 1.25rem', transition: 'background 150ms',
              }}
            >
              {submitting ? t('newDataset.creating') : t('newDataset.create')}
            </button>
          </div>
        </div>
      </form>
    </div>
  )
}
