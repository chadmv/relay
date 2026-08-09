export function formatRelativeTime(iso: string, now: Date = new Date()): string {
  const secs = Math.max(0, Math.round((now.getTime() - new Date(iso).getTime()) / 1000))
  if (secs < 60) return `${secs}s ago`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins}m ago`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.floor(hours / 24)}d ago`
}

// Future-facing sibling of formatRelativeTime. It exists because
// formatRelativeTime clamps with Math.max(0, ...) and suffixes "ago", so it
// renders every future instant as "0s ago". Same injectable-`now` signature so
// callers can drive it from useNow (or a fixed date in tests).
//
// A non-future instant reads "expired" rather than a negative duration, which
// keeps the EXPIRES cell consistent with the EXPIRED status pill derived from the
// same arithmetic.
export function formatTimeUntil(iso: string, now: Date = new Date()): string {
  const secs = Math.round((new Date(iso).getTime() - now.getTime()) / 1000)
  if (secs <= 0) return 'expired'
  if (secs < 60) return `in ${secs}s`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `in ${mins}m`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `in ${hours}h`
  return `in ${Math.floor(hours / 24)}d`
}
