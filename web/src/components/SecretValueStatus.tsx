import { useTranslation } from 'react-i18next'
import type { SecretValueStatus as Status } from '../lib/types'

const MONO = 'var(--font-mono)'

// Shows whether a secret has a value without revealing it: "Set · N chars",
// the preview when one exists (api_key / env_var), or an empty/undecryptable badge.
export default function SecretValueStatus({ status, length, preview }: { status: Status; length: number; preview: string }) {
  const { t } = useTranslation()
  if (status === 'empty') {
    return <span className="badge badge-warning">{t('projectDetail.secrets.statusEmpty')}</span>
  }
  if (status === 'undecryptable') {
    return <span className="badge badge-danger">{t('projectDetail.secrets.statusUndecryptable')}</span>
  }
  return (
    <span
      style={{ fontFamily: MONO, fontSize: '0.75rem', color: 'var(--fg-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: '28rem', display: 'inline-block', verticalAlign: 'middle' }}
      title={preview || undefined}
    >
      {preview
        ? t('projectDetail.secrets.statusSetPreview', { preview, len: length })
        : t('projectDetail.secrets.statusSet', { len: length })}
    </span>
  )
}
