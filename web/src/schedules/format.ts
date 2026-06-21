export { formatRelativeTime } from '../lib/time'

// Forward-looking "in Xs/m/h/d" for next_run_at; "due" once the time has passed.
export function nextRunDisplay(iso: string, now: Date = new Date()): string {
  const secs = Math.round((new Date(iso).getTime() - now.getTime()) / 1000)
  if (secs <= 0) return 'due'
  if (secs < 60) return `in ${secs}s`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `in ${mins}m`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `in ${hours}h`
  return `in ${Math.floor(hours / 24)}d`
}

// First 8 chars of a UUID, or "-" when absent.
export function shortId(id: string | undefined): string {
  if (!id) return '-'
  return id.slice(0, 8)
}
