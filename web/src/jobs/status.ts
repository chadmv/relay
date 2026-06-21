import type { JobStatus } from './api'

interface StatusView {
  text: string
  dot: string
}

// Color mapping for the real jobs.status vocabulary:
// done=ok, running=accent, pending=warn, failed=err,
// everything else (cancelled, unknown) = fg-mute.
export function statusColor(status: JobStatus): StatusView {
  switch (status) {
    case 'done':
      return { text: 'text-ok', dot: 'bg-ok' }
    case 'running':
      return { text: 'text-accent', dot: 'bg-accent' }
    case 'pending':
      return { text: 'text-warn', dot: 'bg-warn' }
    case 'failed':
      return { text: 'text-err', dot: 'bg-err' }
    default:
      return { text: 'text-fg-mute', dot: 'bg-fg-mute' }
  }
}

export function progressPct(done?: number, total?: number): number {
  if (!total || total <= 0) return 0
  return Math.round(((done ?? 0) / total) * 100)
}

// Compact duration between started and finished (or now if still running).
// Returns "-" when the job has not started.
export function formatDuration(startedAt?: string, finishedAt?: string, now = Date.now()): string {
  if (!startedAt) return '-'
  const start = new Date(startedAt).getTime()
  const end = finishedAt ? new Date(finishedAt).getTime() : now
  let secs = Math.max(0, Math.round((end - start) / 1000))
  const h = Math.floor(secs / 3600)
  secs -= h * 3600
  const m = Math.floor(secs / 60)
  secs -= m * 60
  if (h > 0) return `${h}h ${m}m`
  if (m > 0) return `${m}m`
  return `${secs}s`
}

// Short absolute start time, e.g. "Jun 5 · 12:00". Returns "-" when null.
export function formatStarted(startedAt?: string): string {
  if (!startedAt) return '-'
  const d = new Date(startedAt)
  const date = d.toLocaleDateString('en-US', { month: 'short', day: 'numeric' })
  const time = d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false })
  return `${date} · ${time}`
}
