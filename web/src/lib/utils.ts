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

const RESERVED_DOMAIN_PREFIXES = new Set(['www', 'registry', 'traefik', 'muvee'])
const VALID_DOMAIN_PREFIX_RE = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/

export function isValidDomainPrefix(s: string): boolean {
  return !!s && VALID_DOMAIN_PREFIX_RE.test(s) && !RESERVED_DOMAIN_PREFIXES.has(s)
}

export function statusColor(status: string): string {
  switch (status) {
    case 'running': return '#c8f03c'
    case 'building': return '#f0a03c'
    case 'deploying': return '#3cb8f0'
    case 'failed': return '#e05a4e'
    case 'stopped': return '#7a7570'
    default: return '#7a7570'
  }
}
