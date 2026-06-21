import type { Worker, WorkerStatus } from './api'
export { formatRelativeTime } from '../lib/time'

export interface LivenessView {
  label: string
  dotClass: string
  textClass: string
  dimClass: string
}

// Maps the server-authoritative status to presentation. Class strings are
// literals so Tailwind v4 includes them.
export function livenessView(status: WorkerStatus): LivenessView {
  switch (status) {
    case 'online':
      return { label: 'ONLINE', dotClass: 'bg-ok', textClass: 'text-ok', dimClass: '' }
    case 'stale':
      return { label: 'STALE', dotClass: 'bg-warn', textClass: 'text-warn', dimClass: '' }
    case 'disabled':
      return { label: 'DISABLED', dotClass: 'bg-fg-mute', textClass: 'text-fg-mute', dimClass: 'opacity-70' }
    case 'revoked':
      return { label: 'REVOKED', dotClass: 'bg-fg-mute', textClass: 'text-fg-mute', dimClass: 'opacity-70' }
    case 'offline':
      return { label: 'OFFLINE', dotClass: 'bg-err', textClass: 'text-err', dimClass: 'opacity-[0.55]' }
  }
}

export function specLine(w: Worker): string {
  const base = `${w.cpu_cores}c · ${w.ram_gb}GB`
  return w.gpu_count > 0 && w.gpu_model ? `${base} · ${w.gpu_model}` : base
}

export function labelChips(labels: Record<string, string> | null): string[] {
  if (!labels) return []
  return Object.entries(labels).map(([k, v]) => (v ? `${k}=${v}` : k))
}

// Formats a byte count as gibibytes with one decimal, labeled "GB" to match the
// rest of the UI (e.g. worker.ram_gb). Used by the telemetry memory charts.
export function formatGB(bytes: number): string {
  return `${(bytes / 1024 ** 3).toFixed(1)} GB`
}
