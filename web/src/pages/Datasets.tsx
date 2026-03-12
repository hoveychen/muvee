import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { PlusCircle, Scan, Trash2, ChevronRight, FileText, FolderOpen } from 'lucide-react'
import { api } from '../lib/api'
import type { Dataset, DatasetSnapshot, FileHistory } from '../lib/types'
import { formatBytes, timeAgo } from '../lib/utils'

type View = 'list' | 'detail'

export default function Datasets() {
  const [datasets, setDatasets] = useState<Dataset[]>([])
  const [loading, setLoading] = useState(true)
  const [selected, setSelected] = useState<Dataset | null>(null)
  const [view, setView] = useState<View>('list')

  useEffect(() => {
    api.datasets.list().then(setDatasets).finally(() => setLoading(false))
  }, [])

  const handleScan = async (ds: Dataset, e: React.MouseEvent) => {
    e.stopPropagation()
    await api.datasets.scan(ds.id)
  }

  const handleDelete = async (ds: Dataset, e: React.MouseEvent) => {
    e.stopPropagation()
    if (!confirm(`Delete dataset "${ds.name}"?`)) return
    await api.datasets.delete(ds.id)
    setDatasets(prev => prev.filter(d => d.id !== ds.id))
  }

  const openDetail = (ds: Dataset) => {
    setSelected(ds)
    setView('detail')
  }

  if (view === 'detail' && selected) {
    return <DatasetDetail dataset={selected} onBack={() => setView('list')} />
  }

  return (
    <div className="page-enter">
      <div className="flex items-end justify-between mb-10">
        <div>
          <p style={{ fontFamily: 'DM Mono', color: 'var(--fg-muted)', fontSize: '0.7rem', letterSpacing: '0.15em' }}>DATA WAREHOUSE</p>
          <h1 style={{ fontFamily: 'Bebas Neue', fontSize: '3rem', color: 'var(--fg-primary)', lineHeight: 1 }}>Datasets</h1>
        </div>
        <Link
          to="/datasets/new"
          className="flex items-center gap-2 px-4 py-2 rounded-sm text-sm"
          style={{ background: 'var(--accent)', color: '#0f0f0f', fontFamily: 'DM Mono', fontWeight: 500, textDecoration: 'none' }}
        >
          <PlusCircle size={14} /> New Dataset
        </Link>
      </div>

      {loading ? (
        <div style={{ fontFamily: 'DM Mono', color: 'var(--fg-muted)', fontSize: '0.8rem' }}>Loading...</div>
      ) : datasets.length === 0 ? (
        <div className="py-16 text-center">
          <div style={{ fontFamily: 'Bebas Neue', fontSize: '3rem', color: 'var(--border)' }}>NO DATASETS</div>
          <p style={{ fontFamily: 'DM Mono', color: 'var(--fg-muted)', fontSize: '0.8rem', marginTop: '0.5rem' }}>Register an NFS path to start tracking</p>
        </div>
      ) : (
        <table className="w-full border-collapse">
          <thead>
            <tr style={{ borderBottom: '1px solid var(--border)' }}>
              {['NAME', 'NFS PATH', 'SIZE', 'VERSION', 'UPDATED', ''].map(h => (
                <th key={h} style={{
                  fontFamily: 'DM Mono', fontSize: '0.65rem', color: 'var(--fg-muted)',
                  letterSpacing: '0.12em', padding: '0.5rem 1rem', textAlign: 'left', fontWeight: 500,
                }}>
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
                style={{ borderBottom: '1px solid var(--border)', cursor: 'pointer', transition: 'background 100ms' }}
                onMouseEnter={e => { (e.currentTarget as HTMLTableRowElement).style.background = 'var(--bg-hover)' }}
                onMouseLeave={e => { (e.currentTarget as HTMLTableRowElement).style.background = 'transparent' }}
              >
                <td style={{ padding: '0.8rem 1rem' }}>
                  <div style={{ fontFamily: 'Lora', fontSize: '0.9rem', color: 'var(--fg-primary)' }}>{ds.name}</div>
                </td>
                <td style={{ padding: '0.8rem 1rem' }}>
                  <div style={{ fontFamily: 'DM Mono', fontSize: '0.72rem', color: 'var(--fg-muted)', maxWidth: '280px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {ds.nfs_path}
                  </div>
                </td>
                <td style={{ padding: '0.8rem 1rem', textAlign: 'right' }}>
                  <span style={{ fontFamily: 'DM Mono', fontSize: '0.75rem', color: 'var(--fg-primary)' }}>
                    {formatBytes(ds.size_bytes)}
                  </span>
                </td>
                <td style={{ padding: '0.8rem 1rem', textAlign: 'right' }}>
                  <span style={{ fontFamily: 'DM Mono', fontSize: '0.75rem', color: 'var(--accent)' }}>
                    v{ds.version}
                  </span>
                </td>
                <td style={{ padding: '0.8rem 1rem', textAlign: 'right' }}>
                  <span style={{ fontFamily: 'DM Mono', fontSize: '0.72rem', color: 'var(--fg-muted)' }}>
                    {timeAgo(ds.updated_at)}
                  </span>
                </td>
                <td style={{ padding: '0.8rem 0.5rem' }}>
                  <div className="flex items-center gap-1 justify-end">
                    <button
                      onClick={e => handleScan(ds, e)}
                      title="Trigger scan"
                      className="p-1.5 rounded-sm transition-colors"
                      style={{ background: 'none', border: 'none', color: 'var(--fg-muted)', cursor: 'pointer' }}
                      onMouseEnter={e => { (e.currentTarget as HTMLButtonElement).style.color = 'var(--accent)' }}
                      onMouseLeave={e => { (e.currentTarget as HTMLButtonElement).style.color = 'var(--fg-muted)' }}
                    >
                      <Scan size={14} />
                    </button>
                    <button
                      onClick={e => handleDelete(ds, e)}
                      className="p-1.5 rounded-sm transition-colors"
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
      )}
    </div>
  )
}

function DatasetDetail({ dataset, onBack }: { dataset: Dataset; onBack: () => void }) {
  const [snapshots, setSnapshots] = useState<DatasetSnapshot[]>([])
  const [history, setHistory] = useState<FileHistory[]>([])
  const [selectedFile, setSelectedFile] = useState<string | null>(null)
  const [fileHistory, setFileHistory] = useState<FileHistory[]>([])

  useEffect(() => {
    api.datasets.snapshots(dataset.id).then(setSnapshots)
    api.datasets.history(dataset.id).then(setHistory)
  }, [dataset.id])

  const handleFileClick = async (path: string) => {
    setSelectedFile(path)
    const fh = await api.datasets.history(dataset.id, path)
    setFileHistory(fh)
  }

  // Build file tree from history
  const files = Array.from(new Set(history.map(h => h.file_path))).sort()

  const eventColor = (t: FileHistory['event_type']) =>
    t === 'added' ? 'var(--accent)' : t === 'modified' ? '#f0a03c' : 'var(--danger)'

  return (
    <div className="page-enter">
      <button
        onClick={onBack}
        style={{ fontFamily: 'DM Mono', fontSize: '0.75rem', color: 'var(--fg-muted)', background: 'none', border: 'none', cursor: 'pointer', marginBottom: '1.5rem', display: 'flex', alignItems: 'center', gap: '4px' }}
      >
        ← Back to Datasets
      </button>

      <div className="flex items-start justify-between mb-8">
        <div>
          <h1 style={{ fontFamily: 'Bebas Neue', fontSize: '3rem', color: 'var(--fg-primary)', lineHeight: 1 }}>{dataset.name}</h1>
          <div style={{ fontFamily: 'DM Mono', fontSize: '0.72rem', color: 'var(--fg-muted)', marginTop: '0.3rem' }}>{dataset.nfs_path}</div>
        </div>
        <div className="text-right">
          <div style={{ fontFamily: 'Bebas Neue', fontSize: '1.8rem', color: 'var(--accent)' }}>v{dataset.version}</div>
          <div style={{ fontFamily: 'DM Mono', fontSize: '0.7rem', color: 'var(--fg-muted)' }}>{formatBytes(dataset.size_bytes)}</div>
        </div>
      </div>

      {/* Snapshot history */}
      {snapshots.length > 0 && (
        <div className="mb-8">
          <p style={{ fontFamily: 'DM Mono', fontSize: '0.65rem', color: 'var(--fg-muted)', letterSpacing: '0.12em', marginBottom: '0.75rem' }}>SCAN HISTORY</p>
          <div className="flex gap-2 overflow-x-auto pb-2">
            {snapshots.slice(0, 10).map(snap => (
              <div key={snap.id} className="flex-shrink-0 px-3 py-2 rounded-sm" style={{ background: 'var(--bg-card)', border: '1px solid var(--border)', minWidth: '130px' }}>
                <div style={{ fontFamily: 'DM Mono', fontSize: '0.65rem', color: 'var(--accent)' }}>v{snap.version}</div>
                <div style={{ fontFamily: 'DM Mono', fontSize: '0.68rem', color: 'var(--fg-muted)', marginTop: '2px' }}>{snap.total_files.toLocaleString()} files</div>
                <div style={{ fontFamily: 'DM Mono', fontSize: '0.65rem', color: 'var(--fg-muted)' }}>{timeAgo(snap.scanned_at)}</div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Split: file tree + history */}
      <div className="flex gap-px" style={{ background: 'var(--border)', height: '500px' }}>
        {/* File tree */}
        <div className="w-1/3 overflow-y-auto" style={{ background: 'var(--bg-card)' }}>
          <div className="px-4 py-3" style={{ borderBottom: '1px solid var(--border)', fontFamily: 'DM Mono', fontSize: '0.65rem', color: 'var(--fg-muted)', letterSpacing: '0.12em' }}>
            FILES ({files.length})
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
              <span style={{ fontFamily: 'DM Mono', fontSize: '0.7rem', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                {path}
              </span>
            </button>
          ))}
          {files.length === 0 && (
            <div className="py-8 text-center" style={{ fontFamily: 'DM Mono', fontSize: '0.75rem', color: 'var(--fg-muted)' }}>
              <FolderOpen size={24} style={{ margin: '0 auto 0.5rem' }} />
              No history yet
            </div>
          )}
        </div>

        {/* History timeline */}
        <div className="flex-1 overflow-y-auto" style={{ background: 'var(--bg-card)' }}>
          <div className="px-5 py-3" style={{ borderBottom: '1px solid var(--border)', fontFamily: 'DM Mono', fontSize: '0.65rem', color: 'var(--fg-muted)', letterSpacing: '0.12em' }}>
            {selectedFile ? `HISTORY: ${selectedFile}` : 'ALL CHANGES'}
          </div>
          {(selectedFile ? fileHistory : history).length === 0 ? (
            <div className="py-12 text-center" style={{ fontFamily: 'DM Mono', fontSize: '0.75rem', color: 'var(--fg-muted)' }}>
              {selectedFile ? 'No history for this file' : 'No changes recorded yet'}
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
                    boxShadow: `0 0 0 2px ${eventColor(h.event_type)}44`,
                  }} />
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-3 mb-1">
                      <span style={{
                        fontFamily: 'DM Mono', fontSize: '0.65rem',
                        color: eventColor(h.event_type),
                        background: `${eventColor(h.event_type)}11`,
                        border: `1px solid ${eventColor(h.event_type)}33`,
                        padding: '1px 6px', borderRadius: '2px',
                      }}>
                        {h.event_type.toUpperCase()}
                      </span>
                      <span style={{ fontFamily: 'DM Mono', fontSize: '0.68rem', color: 'var(--fg-muted)' }}>
                        {timeAgo(h.occurred_at)}
                      </span>
                    </div>
                    {!selectedFile && (
                      <div style={{ fontFamily: 'DM Mono', fontSize: '0.72rem', color: 'var(--fg-primary)', marginBottom: '0.3rem', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                        {h.file_path}
                      </div>
                    )}
                    {h.event_type !== 'deleted' && (
                      <div className="flex gap-4" style={{ fontFamily: 'DM Mono', fontSize: '0.68rem', color: 'var(--fg-muted)' }}>
                        <span>{formatBytes(h.new_size)}</span>
                        {h.new_checksum && (
                          <span style={{ color: 'var(--border)' }}>
                            sha256:{h.new_checksum.slice(0, 12)}…
                          </span>
                        )}
                      </div>
                    )}
                    {h.event_type === 'modified' && h.old_checksum && (
                      <div style={{ fontFamily: 'DM Mono', fontSize: '0.65rem', color: 'var(--fg-muted)', marginTop: '2px' }}>
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
