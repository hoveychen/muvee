import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { PlusCircle, Scan, Trash2, ChevronRight, FileText, FolderOpen } from 'lucide-react'
import { api } from '../lib/api'
import type { Dataset, DatasetSnapshot, FileHistory } from '../lib/types'
import { formatBytes, resolveDatasetPath, timeAgo } from '../lib/utils'
import { useTranslation } from 'react-i18next'

const MONO = 'var(--font-mono)'

type View = 'list' | 'detail'

function eventBadgeClass(eventType: FileHistory['event_type']): string {
  switch (eventType) {
    case 'added': return 'badge badge-success'
    case 'modified': return 'badge badge-warning'
    case 'deleted': return 'badge badge-danger'
    default: return 'badge badge-neutral'
  }
}

function eventColor(eventType: FileHistory['event_type']): string {
  switch (eventType) {
    case 'added': return 'var(--success)'
    case 'modified': return 'var(--warning)'
    case 'deleted': return 'var(--danger)'
    default: return 'var(--fg-muted)'
  }
}

export default function Datasets() {
  const [datasets, setDatasets] = useState<Dataset[]>([])
  const [loading, setLoading] = useState(true)
  const [datasetBasePath, setDatasetBasePath] = useState('')
  const [selected, setSelected] = useState<Dataset | null>(null)
  const [view, setView] = useState<View>('list')
  const { t } = useTranslation()

  useEffect(() => {
    api.datasets.list().then(setDatasets).finally(() => setLoading(false))
    api.runtime.config()
      .then(cfg => setDatasetBasePath(cfg.dataset_nfs_base_path || ''))
      .catch(() => setDatasetBasePath(''))
  }, [])

  const handleScan = async (ds: Dataset, e: React.MouseEvent) => {
    e.stopPropagation()
    await api.datasets.scan(ds.id)
  }

  const handleDelete = async (ds: Dataset, e: React.MouseEvent) => {
    e.stopPropagation()
    if (!confirm(t('datasets.deleteConfirm', { name: ds.name }))) return
    await api.datasets.delete(ds.id)
    setDatasets(prev => prev.filter(d => d.id !== ds.id))
  }

  const openDetail = (ds: Dataset) => {
    setSelected(ds)
    setView('detail')
  }

  if (view === 'detail' && selected) {
    return <DatasetDetail dataset={selected} datasetBasePath={datasetBasePath} onBack={() => setView('list')} />
  }

  return (
    <div className="page-enter">
      <div className="page-header flex items-end justify-between">
        <div>
          <p className="page-subtitle">{t('datasets.sectionLabel')}</p>
          <h1 className="page-title">{t('datasets.heading')}</h1>
        </div>
        <Link
          to="/datasets/new"
          className="btn-primary flex items-center gap-2"
          style={{ textDecoration: 'none' }}
        >
          <PlusCircle size={14} /> {t('datasets.newDataset')}
        </Link>
      </div>

      {loading ? (
        <div style={{ color: 'var(--fg-muted)', fontSize: '0.875rem' }}>{t('datasets.loading')}</div>
      ) : datasets.length === 0 ? (
        <div className="py-16 text-center">
          <div style={{ fontSize: '2rem', fontWeight: 700, color: 'var(--border)' }}>{t('datasets.empty')}</div>
          <p style={{ color: 'var(--fg-muted)', fontSize: '0.875rem', marginTop: '0.5rem' }}>{t('datasets.emptyHint')}</p>
        </div>
      ) : (
        <div className="table-container">
          <table className="w-full border-collapse">
            <thead>
              <tr>
                {[
                  t('datasets.columns.name'),
                  t('datasets.columns.nfsPath'),
                  t('datasets.columns.size'),
                  t('datasets.columns.version'),
                  t('datasets.columns.updated'),
                  '',
                ].map(h => (
                  <th key={h}>
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {datasets.map(ds => (
                <tr
                  key={ds.id}
                  onClick={() => openDetail(ds)}
                  style={{ cursor: 'pointer' }}
                >
                  <td style={{ padding: '0.75rem 1rem' }}>
                    <div style={{ fontSize: '0.875rem', fontWeight: 500, color: 'var(--fg-primary)' }}>{ds.name}</div>
                  </td>
                  <td style={{ padding: '0.75rem 1rem' }}>
                    <div style={{ fontFamily: MONO, fontSize: '0.8125rem', color: 'var(--fg-muted)', maxWidth: '280px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {resolveDatasetPath(datasetBasePath, ds.nfs_path)}
                    </div>
                  </td>
                  <td style={{ padding: '0.75rem 1rem', textAlign: 'right' }}>
                    <span style={{ fontFamily: MONO, fontSize: '0.8125rem', color: 'var(--fg-primary)' }}>
                      {formatBytes(ds.size_bytes)}
                    </span>
                  </td>
                  <td style={{ padding: '0.75rem 1rem', textAlign: 'right' }}>
                    <span className="badge badge-info">
                      v{ds.version}
                    </span>
                  </td>
                  <td style={{ padding: '0.75rem 1rem', textAlign: 'right' }}>
                    <span style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>
                      {timeAgo(ds.updated_at)}
                    </span>
                  </td>
                  <td style={{ padding: '0.75rem 0.5rem' }}>
                    <div className="flex items-center gap-1 justify-end">
                      <button
                        onClick={e => handleScan(ds, e)}
                        title={t('datasets.triggerScan')}
                        className="p-1.5 rounded-md transition-colors"
                        style={{ background: 'none', border: 'none', color: 'var(--fg-muted)', cursor: 'pointer' }}
                        onMouseEnter={e => { (e.currentTarget as HTMLButtonElement).style.color = 'var(--accent)' }}
                        onMouseLeave={e => { (e.currentTarget as HTMLButtonElement).style.color = 'var(--fg-muted)' }}
                      >
                        <Scan size={14} />
                      </button>
                      <button
                        onClick={e => handleDelete(ds, e)}
                        className="p-1.5 rounded-md transition-colors"
                        style={{ background: 'none', border: 'none', color: 'var(--fg-muted)', cursor: 'pointer' }}
                        onMouseEnter={e => { (e.currentTarget as HTMLButtonElement).style.color = 'var(--danger)' }}
                        onMouseLeave={e => { (e.currentTarget as HTMLButtonElement).style.color = 'var(--fg-muted)' }}
                      >
                        <Trash2 size={14} />
                      </button>
                      <ChevronRight size={14} style={{ color: 'var(--border)' }} />
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

function DatasetDetail({ dataset, datasetBasePath, onBack }: { dataset: Dataset; datasetBasePath: string; onBack: () => void }) {
  const [snapshots, setSnapshots] = useState<DatasetSnapshot[]>([])
  const [history, setHistory] = useState<FileHistory[]>([])
  const [selectedFile, setSelectedFile] = useState<string | null>(null)
  const [fileHistory, setFileHistory] = useState<FileHistory[]>([])
  const { t } = useTranslation()

  useEffect(() => {
    api.datasets.snapshots(dataset.id).then(setSnapshots)
    api.datasets.history(dataset.id).then(setHistory)
  }, [dataset.id])

  const handleFileClick = async (path: string) => {
    setSelectedFile(path)
    const fh = await api.datasets.history(dataset.id, path)
    setFileHistory(fh)
  }

  const files = Array.from(new Set(history.map(h => h.file_path))).sort()

  return (
    <div className="page-enter">
      <button
        onClick={onBack}
        className="btn-secondary"
        style={{ marginBottom: '1.5rem', display: 'flex', alignItems: 'center', gap: '4px' }}
      >
        {t('datasets.backToDatasets')}
      </button>

      <div className="page-header flex items-start justify-between">
        <div>
          <h1 className="page-title">{dataset.name}</h1>
          <div style={{ fontFamily: MONO, fontSize: '0.8125rem', color: 'var(--fg-muted)', marginTop: '0.3rem' }}>
            {resolveDatasetPath(datasetBasePath, dataset.nfs_path)}
          </div>
        </div>
        <div className="text-right">
          <div style={{ fontFamily: MONO, fontSize: '1.4rem', fontWeight: 700, color: 'var(--accent)' }}>v{dataset.version}</div>
          <div style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>{formatBytes(dataset.size_bytes)}</div>
        </div>
      </div>

      {/* Snapshot history */}
      {snapshots.length > 0 && (
        <div className="mb-8">
          <p style={{ fontSize: '0.8125rem', fontWeight: 500, color: 'var(--fg-muted)', letterSpacing: '0.04em', marginBottom: '0.75rem' }}>{t('datasets.scanHistory')}</p>
          <div className="flex gap-2 overflow-x-auto pb-2">
            {snapshots.slice(0, 10).map(snap => (
              <div key={snap.id} className="card flex-shrink-0 px-3 py-2" style={{ minWidth: '130px' }}>
                <div style={{ fontFamily: MONO, fontSize: '0.8125rem', color: 'var(--accent)' }}>v{snap.version}</div>
                <div style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginTop: '2px' }}>{snap.total_files.toLocaleString()} {t('datasets.files')}</div>
                <div style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>{timeAgo(snap.scanned_at)}</div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Split: file tree + history */}
      <div className="card flex gap-px" style={{ overflow: 'hidden', height: '500px' }}>
        {/* File tree */}
        <div className="w-1/3 overflow-y-auto" style={{ background: 'var(--bg-card)' }}>
          <div className="px-4 py-3" style={{ borderBottom: '1px solid var(--border)', fontSize: '0.8125rem', fontWeight: 500, color: 'var(--fg-muted)', letterSpacing: '0.04em' }}>
            {t('datasets.filesHeader')} ({files.length})
          </div>
          {files.map(path => (
            <button
              key={path}
              onClick={() => handleFileClick(path)}
              className="w-full flex items-center gap-2 px-4 py-2 text-left transition-colors"
              style={{
                background: selectedFile === path ? 'var(--bg-hover)' : 'none',
                border: 'none',
                borderLeft: selectedFile === path ? '2px solid var(--accent)' : '2px solid transparent',
                cursor: 'pointer',
                color: 'var(--fg-primary)',
              }}
            >
              <FileText size={11} style={{ color: 'var(--fg-muted)', flexShrink: 0 }} />
              <span style={{ fontFamily: MONO, fontSize: '0.8125rem', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                {path}
              </span>
            </button>
          ))}
          {files.length === 0 && (
            <div className="py-8 text-center" style={{ fontSize: '0.875rem', color: 'var(--fg-muted)' }}>
              <FolderOpen size={24} style={{ margin: '0 auto 0.5rem' }} />
              {t('datasets.noHistory')}
            </div>
          )}
        </div>

        {/* History timeline */}
        <div className="flex-1 overflow-y-auto" style={{ background: 'var(--bg-card)', borderLeft: '1px solid var(--border)' }}>
          <div className="px-5 py-3" style={{ borderBottom: '1px solid var(--border)', fontSize: '0.8125rem', fontWeight: 500, color: 'var(--fg-muted)', letterSpacing: '0.04em' }}>
            {selectedFile ? t('datasets.fileHistory', { file: selectedFile }) : t('datasets.allChanges')}
          </div>
          {(selectedFile ? fileHistory : history).length === 0 ? (
            <div className="py-12 text-center" style={{ fontSize: '0.875rem', color: 'var(--fg-muted)' }}>
              {selectedFile ? t('datasets.noFileHistory') : t('datasets.noChanges')}
            </div>
          ) : (
            <div className="relative">
              {/* Timeline line */}
              <div className="absolute left-[2.25rem] top-0 bottom-0 w-px" style={{ background: 'var(--border)' }} />
              {(selectedFile ? fileHistory : history).map((h, i) => (
                <div key={h.id} className="flex gap-4 px-5 py-4" style={{ borderBottom: '1px solid var(--border)', animationDelay: `${i * 20}ms` }}>
                  {/* Event dot */}
                  <div className="flex-shrink-0 mt-1 relative z-10" style={{
                    width: '10px', height: '10px', borderRadius: '50%',
                    background: eventColor(h.event_type),
                    border: '2px solid var(--bg-card)',
                    boxShadow: `0 0 0 2px color-mix(in srgb, ${eventColor(h.event_type)} 25%, transparent)`,
                  }} />
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-3 mb-1">
                      <span className={eventBadgeClass(h.event_type)}>
                        {h.event_type}
                      </span>
                      <span style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>
                        {timeAgo(h.occurred_at)}
                      </span>
                    </div>
                    {!selectedFile && (
                      <div style={{ fontFamily: MONO, fontSize: '0.8125rem', color: 'var(--fg-primary)', marginBottom: '0.3rem', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                        {h.file_path}
                      </div>
                    )}
                    {h.event_type !== 'deleted' && (
                      <div className="flex gap-4" style={{ fontFamily: MONO, fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>
                        <span>{formatBytes(h.new_size)}</span>
                        {h.new_checksum && (
                          <span style={{ color: 'var(--border)' }}>
                            sha256:{h.new_checksum.slice(0, 12)}…
                          </span>
                        )}
                      </div>
                    )}
                    {h.event_type === 'modified' && h.old_checksum && (
                      <div style={{ fontFamily: MONO, fontSize: '0.8125rem', color: 'var(--fg-muted)', marginTop: '2px' }}>
                        was: {h.old_checksum.slice(0, 12)}… ({formatBytes(h.old_size)})
                      </div>
                    )}
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
