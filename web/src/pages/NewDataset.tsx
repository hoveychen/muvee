import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '../lib/api'

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

  const canSubmit = name.trim() !== '' && nfsPath.trim() !== ''

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
      setError(err instanceof Error ? err.message : 'Failed to create dataset')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="page-enter" style={{ maxWidth: '560px' }}>
      <div style={{ marginBottom: '2rem' }}>
        <p style={{ fontFamily: MONO, color: 'var(--fg-muted)', fontSize: '0.72rem', letterSpacing: '0.05em' }}>DATA WAREHOUSE</p>
        <h1 style={{ fontSize: '1.75rem', fontWeight: 700, color: 'var(--fg-primary)', lineHeight: 1.2, marginTop: '4px' }}>New Dataset</h1>
      </div>

      <form onSubmit={handleSubmit}>
        <div style={{ border: '1px solid var(--border)', borderRadius: '6px', overflow: 'hidden', background: 'var(--bg-card)' }}>
          <div style={{ padding: '1.5rem', borderBottom: '1px solid var(--border)' }}>
            <div style={{ marginBottom: '1.25rem' }}>
              <label style={labelStyle}>NAME</label>
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
              <label style={labelStyle}>NFS PATH</label>
              <input
                style={inputStyle}
                value={nfsPath}
                onChange={e => setNfsPath(e.target.value)}
                onFocus={focusAccent}
                onBlur={blurBorder}
                placeholder="/mnt/nfs/data"
              />
              <p style={{ fontFamily: MONO, fontSize: '0.7rem', color: 'var(--fg-muted)', marginTop: '0.4rem' }}>
                挂载到节点上的 NFS 绝对路径
              </p>
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
              取消
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
              {submitting ? '创建中...' : '创建 Dataset'}
            </button>
          </div>
        </div>
      </form>
    </div>
  )
}
