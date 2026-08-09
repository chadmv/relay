import { formatTimeUntil } from '../../lib/time'

export type EnrollmentStatus = 'ACTIVE' | 'EXPIRING' | 'EXPIRED'

const EXPIRING_WINDOW_MS = 60 * 60 * 1000

// Every list and count query filters `consumed_at IS NULL AND expires_at > NOW()`
// (internal/store/query/agent_enrollments.sql:26-27, :35, :40-41, :50-51, :60-61)
// and no row carries a status field, so the ONLY server-asserted fact about a
// returned row is "unconsumed and unexpired as of query time". Status is therefore
// arithmetic on expires_at and the browser clock, and nothing else is honestly
// derivable:
//
//  - There is deliberately no CONSUMED state. Consumption sets consumed_at and the
//    row simply vanishes from the list; inventing the state would be faking data.
//  - EXPIRED is reachable only for a row already on screen when its expiry passes.
//    The query never returns one. Rendering it as ACTIVE would be a lie the client
//    can disprove with arithmetic it already has.
//  - This reads the local clock, so a badly skewed browser mislabels a row.
//    Accepted: the server exposes no status to prefer instead.
export function deriveStatus(expiresAt: string, now: Date): EnrollmentStatus {
  const remaining = new Date(expiresAt).getTime() - now.getTime()
  if (remaining <= 0) return 'EXPIRED'
  if (remaining < EXPIRING_WINDOW_MS) return 'EXPIRING'
  return 'ACTIVE'
}

// The three tones Chip already ships (web/src/components/holo/Chip.tsx:8-12).
export function statusTone(status: EnrollmentStatus): 'accent' | 'warn' | 'muted' {
  if (status === 'EXPIRED') return 'muted'
  if (status === 'EXPIRING') return 'warn'
  return 'accent'
}

// EnrollmentsTable's EXPIRES cell uses this instead of calling formatTimeUntil
// directly. The row's `now` is useNow(60_000) - a local clock tick refreshed
// once a MINUTE - so a seconds-precision label such as "in 20s" is only accurate
// at the instant of the tick; for up to 59 more real seconds the row's actual
// remaining time keeps falling while the label stays frozen, so a row can read
// "in 20s" / EXPIRING for nearly a minute after it has genuinely expired.
// Collapsing anything under a minute to "in <1m" means the displayed precision
// never promises more freshness than the 60s refresh cadence actually delivers.
// formatTimeUntil itself is left general-purpose (full seconds precision) for
// callers that render from a fresh `now`, e.g. a one-shot render in a dialog.
export function formatExpiryLabel(expiresAt: string, now: Date): string {
  const label = formatTimeUntil(expiresAt, now)
  return /^in \d+s$/.test(label) ? 'in <1m' : label
}
