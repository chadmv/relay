import { formatTimeUntil } from '../../lib/time'

export type InviteStatus = 'REDEEMED' | 'EXPIRED' | 'EXPIRING' | 'ACTIVE'

// Duplicated from enrollmentStatus.ts:5 rather than shared. SECOND consumer; the
// repo rule is extract before the THIRD, so a third status module must lift this
// constant AND formatExpiryLabel below into a shared web/src/lib/expiry.ts. Cited
// rather than invented: README.md:1300-1303 documents the 1h window as the
// shipped contract.
const EXPIRING_WINDOW_MS = 60 * 60 * 1000

// The list endpoint returns FACTS and no status field, by design - the handler
// says so at internal/api/invites.go:112-121 and the query projection selects
// seven columns, none of them a status (internal/store/query/invites.sql:31-32).
// A server-asserted "expired" is stale the moment the row is on screen, and
// "expiring" needs an invented threshold. So the pill is the client's arithmetic
// over expires_at and used_at.
//
// The order below is LOAD-BEARING and matches README.md:1300-1303:
//
//  1. REDEEMED first. Redemption is terminal and one-way: MarkInviteUsed is the
//     only writer and carries `AND used_at IS NULL` (invites.sql:9-12), called
//     once from registration (internal/api/auth.go:147-158). A redeemed invite
//     that later passes its expiry is STILL redeemed - both facts are on the row,
//     and redemption is the one that describes what happened to the credential.
//     It is also the only state that is immune to clock skew, because it derives
//     from a server-written timestamp's PRESENCE, not from a comparison.
//  2. EXPIRED at `remaining <= 0` on the raw millisecond delta, byte-identical to
//     enrollmentStatus.ts:23 and to formatTimeUntil's own boundary
//     (web/src/lib/time.ts:29), so the pill and the EXPIRES cell flip at the same
//     instant.
//  3. EXPIRING strictly under the window: 59m59s is EXPIRING, exactly 1h00m00s is
//     ACTIVE.
//  4. ACTIVE otherwise.
//
// This reads the local clock, so a badly skewed browser mislabels EXPIRING and
// EXPIRED. Accepted for the same reason enrollmentStatus.ts:19-20 accepts it: the
// server exposes no status to prefer instead.
//
// The parameter is the ROW SHAPE, not a bare string, because the derivation needs
// two fields. used_at is optional-not-nullable (invites.go:142-144), so the check
// is against undefined; `used_at !== null` would be always-true.
export interface InviteStatusInput {
  expires_at: string
  used_at?: string
}

export function deriveStatus(invite: InviteStatusInput, now: Date): InviteStatus {
  if (invite.used_at !== undefined) return 'REDEEMED'
  const remaining = new Date(invite.expires_at).getTime() - now.getTime()
  if (remaining <= 0) return 'EXPIRED'
  if (remaining < EXPIRING_WINDOW_MS) return 'EXPIRING'
  return 'ACTIVE'
}

// Four states, four Chip tones (web/src/components/holo/Chip.tsx:8-13, where
// `err` was added for this tab). Colour is never the only channel: the pill TEXT
// differs per state and both terminal states also dim their row.
export function statusTone(status: InviteStatus): 'accent' | 'warn' | 'muted' | 'err' {
  if (status === 'REDEEMED') return 'muted'
  if (status === 'EXPIRED') return 'err'
  if (status === 'EXPIRING') return 'warn'
  return 'accent'
}

// Duplicated verbatim from enrollmentStatus.ts:45-48, reasoning included, because
// the reasoning is what stops it being "simplified" back to formatTimeUntil:
// the row's `now` is useNow(60_000) - a local clock tick refreshed once a MINUTE -
// so a seconds-precision label such as "in 20s" is only accurate at the instant of
// the tick; for up to 59 more real seconds the row's actual remaining time keeps
// falling while the label stays frozen, so a row can read "in 20s" / EXPIRING for
// nearly a minute after it has genuinely expired. Collapsing anything under a
// minute to "in <1m" means the displayed precision never promises more freshness
// than the 60s refresh cadence actually delivers.
//
// SECOND consumer of this exact body. Extract before the third, into
// web/src/lib/expiry.ts together with EXPIRING_WINDOW_MS above.
export function formatExpiryLabel(expiresAt: string, now: Date): string {
  const label = formatTimeUntil(expiresAt, now)
  return /^in \d+s$/.test(label) ? 'in <1m' : label
}
