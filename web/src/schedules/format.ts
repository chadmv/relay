// Relative "Xs/m/h/d ago" for a past timestamp. Mirrors the Workers helper.
export function formatRelativeTime(iso: string, now: Date = new Date()): string {
  const secs = Math.max(0, Math.round((now.getTime() - new Date(iso).getTime()) / 1000))
  if (secs < 60) return `${secs}s ago`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins}m ago`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.floor(hours / 24)}d ago`
}

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
