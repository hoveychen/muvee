import { type ClassValue, clsx } from 'clsx'
import { twMerge } from 'tailwind-merge'

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i]
}

export function timeAgo(dateStr: string): string {
  const date = new Date(dateStr)
  const now = new Date()
  const diff = Math.floor((now.getTime() - date.getTime()) / 1000)
  if (diff < 60) return `${diff}s ago`
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`
  return `${Math.floor(diff / 86400)}d ago`
}

const RESERVED_DOMAIN_PREFIXES = new Set(['registry', 'traefik', 'muvee'])
const VALID_DOMAIN_PREFIX_RE = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/

export function isValidDomainPrefix(s: string): boolean {
  return !!s && VALID_DOMAIN_PREFIX_RE.test(s) && !RESERVED_DOMAIN_PREFIXES.has(s)
}

export function statusColor(status: string): string {
  switch (status) {
    case 'running': return 'var(--success)'
    case 'building': return 'var(--warning)'
    case 'deploying': return 'var(--accent)'
    case 'failed': return 'var(--danger)'
    case 'stopped': return 'var(--fg-muted)'
    default: return 'var(--fg-muted)'
  }
}

export function resolveDatasetPath(basePath: string, subPath: string): string {
  if (!subPath) return ''
  if (subPath.startsWith('/')) return subPath
  if (!basePath) return subPath
  const base = basePath.replace(/\/+$/, '')
  const sub = subPath.replace(/^\/+/, '')
  return `${base}/${sub}`
}
