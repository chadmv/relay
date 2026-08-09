export type ReservationStatus = 'ACTIVE' | 'SCHEDULED' | 'ENDED'

// `reservations` has NO lifecycle column - no status, consumed, released or deleted
// field. The row is read in exactly one place, Dispatcher.selectWorker, via
// ListActiveReservations (internal/store/query/reservations.sql:19-23):
//
//   WHERE (ends_at IS NULL OR ends_at > NOW())
//     AND (starts_at IS NULL OR starts_at <= NOW())
//
// GET /v1/reservations is UNFILTERED (CountReservations is a bare COUNT(*), and the
// page queries filter on the cursor only), so unlike the enrollments list this
// client sees past, current and future rows and can reproduce that predicate
// exactly. ACTIVE here means "in the dispatcher's reservedIDs set right now".
//
// Comparison directions are load-bearing: `<=` on starts_at and a strict `>` on
// ends_at, matching the SQL. ENDED is tested FIRST so that an inverted window
// (ends_at < starts_at, which the server accepts and which can never be active)
// reads as dead rather than pending.
//
// Reads the local clock, so a badly skewed browser mislabels a row. Accepted for
// the same reason as enrollmentStatus.ts: the server exposes no status to prefer.
export function deriveStatus(
  r: { starts_at?: string; ends_at?: string },
  now: Date,
): ReservationStatus {
  const t = now.getTime()
  const startsMs = r.starts_at !== undefined ? new Date(r.starts_at).getTime() : undefined
  const endsMs = r.ends_at !== undefined ? new Date(r.ends_at).getTime() : undefined
  // An inverted or zero-length window (ends_at <= starts_at) can NEVER satisfy
  // ListActiveReservations regardless of `now` - comparing each bound to `now`
  // independently is not enough, because a window entirely in the future on both
  // sides (starts_at AND ends_at both after now, but ends_at before starts_at)
  // would otherwise read as SCHEDULED forever. Checked first so it reads as dead
  // rather than pending.
  if (startsMs !== undefined && endsMs !== undefined && endsMs <= startsMs) return 'ENDED'
  if (endsMs !== undefined && endsMs <= t) return 'ENDED'
  if (startsMs !== undefined && startsMs > t) return 'SCHEDULED'
  return 'ACTIVE'
}

// The three tones Chip already ships (web/src/components/holo/Chip.tsx:8-12).
export function statusTone(status: ReservationStatus): 'accent' | 'warn' | 'muted' {
  if (status === 'ENDED') return 'muted'
  if (status === 'SCHEDULED') return 'warn'
  return 'accent'
}
