import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '../lib/api'
import { useTranslation } from 'react-i18next'
import { resolveDatasetPath } from '../lib/utils'

const MONO = 'var(--font-mono)'

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
      <div className="page-header">
        <h1 className="page-title">{t('newDataset.heading')}</h1>
        <p className="page-subtitle">{t('newDataset.sectionLabel')}</p>
      </div>

      <form onSubmit={handleSubmit}>
        <div className="card">
          <div style={{ padding: '24px', borderBottom: '1px solid var(--border)' }}>
            <div style={{ marginBottom: '20px' }}>
              <label className="form-label">{t('newDataset.name')}</label>
              <input
                className="form-input"
                style={{ fontFamily: MONO }}
                value={name}
                onChange={e => setName(e.target.value)}
                placeholder="my-dataset"
                autoFocus
              />
            </div>

            <div>
              <label className="form-label">{t('newDataset.nfsPath')}</label>
              <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginBottom: '6px' }}>
                {t('newDataset.datasetBasePath')}: <span style={{ color: 'var(--fg-primary)', fontFamily: MONO }}>{datasetBasePath || t('newDataset.notConfigured')}</span>
              </p>
              <input
                className="form-input"
                style={{ fontFamily: MONO }}
                value={nfsPath}
                onChange={e => setNfsPath(e.target.value)}
                placeholder="warehouse"
              />
              <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginTop: '6px' }}>
                {t('newDataset.nfsPathHint')}
              </p>
              {nfsPath.trim() !== '' && (
                <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginTop: '4px' }}>
                  {t('newDataset.fullPathPreview')}: <span style={{ color: 'var(--fg-primary)', fontFamily: MONO }}>{resolveDatasetPath(datasetBasePath, nfsPath.trim())}</span>
                </p>
              )}
            </div>
          </div>

          {error && (
            <div style={{ padding: '12px 24px', background: 'rgba(220,38,38,0.06)', borderBottom: '1px solid var(--border)' }}>
              <p style={{ fontSize: '0.875rem', color: 'var(--danger)' }}>{error}</p>
            </div>
          )}

          <div className="flex items-center justify-end gap-3" style={{ padding: '16px 24px' }}>
            <button
              type="button"
              onClick={() => navigate('/datasets')}
              className="btn-secondary"
            >
              {t('newDataset.cancel')}
            </button>
            <button
              type="submit"
              disabled={!canSubmit || submitting}
              className="btn-primary"
            >
              {submitting ? t('newDataset.creating') : t('newDataset.create')}
            </button>
          </div>
        </div>
      </form>
    </div>
  )
}
