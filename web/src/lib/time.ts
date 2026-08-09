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
// keeps the EXPIRES cell consistent with the EXPIRED status pill: both derive
// from the SAME raw millisecond delta (target - now) and both expire at exactly
// remaining <= 0ms (enrollmentStatus.deriveStatus, enrollmentStatus.ts:19-24).
// The "expired" check therefore runs on the raw ms diff, before any rounding for
// display - an earlier version rounded to whole seconds first and then checked
// secs <= 0, which rounded any positive sub-500ms remainder down to 0 and
// reported it "expired" while deriveStatus (operating on the same instant) still
// called it EXPIRING. Flooring the remainder for display (rather than rounding)
// only ever loses precision on values already confirmed positive, so it cannot
// reopen that gap.
export function formatTimeUntil(iso: string, now: Date = new Date()): string {
  const remainingMs = new Date(iso).getTime() - now.getTime()
  if (remainingMs <= 0) return 'expired'
  const secs = Math.floor(remainingMs / 1000)
  if (secs < 60) return `in ${secs}s`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `in ${mins}m`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `in ${hours}h`
  return `in ${Math.floor(hours / 24)}d`
}

// Absolute LOCAL wall-clock rendering, 'YYYY-MM-DD HH:MM'.
//
// Local, not UTC, because the only writers of these values are the admin's own
// `datetime-local` inputs (web/src/admin/reservations/CreateReservationForm.tsx):
// the cell must read back what they typed.
//
// Built from Date getters rather than toLocaleString/Intl on purpose. Intl output
// depends on the runner's ICU locale, which makes it both unassertable in tests
// and inconsistent across machines; getters give one fixed shape everywhere.
export function formatDateTime(iso: string): string {
  const d = new Date(iso)
  // Not reachable from the Go server (which only ever emits well-formed RFC3339),
  // but a hand-edited row via SQL/CLI/MCP could carry garbage. Without this guard
  // the cell would render the literal 'NaN-NaN-NaN NaN:NaN' rather than the same
  // dash placeholder used for every other absent/unparseable value in this table.
  if (Number.isNaN(d.getTime())) return '-'
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
