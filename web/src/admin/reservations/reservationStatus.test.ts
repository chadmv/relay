import { expect, test } from 'vitest'
import { deriveStatus, statusTone } from './reservationStatus'

const NOW = new Date('2026-08-09T12:00:00Z')

test('both bounds absent is ACTIVE (an open-ended reservation)', () => {
  expect(deriveStatus({}, NOW)).toBe('ACTIVE')
})

test('open start with a future end is ACTIVE', () => {
  expect(deriveStatus({ ends_at: '2026-08-09T13:00:00Z' }, NOW)).toBe('ACTIVE')
})

test('open end with a past start is ACTIVE', () => {
  expect(deriveStatus({ starts_at: '2026-08-09T11:00:00Z' }, NOW)).toBe('ACTIVE')
})

// The two boundary tests that matter. The scheduler's predicate is
// `starts_at <= NOW()` AND `ends_at > NOW()` (internal/store/query/reservations.sql:21-22),
// so at exactly starts_at the row IS active and at exactly ends_at it is NOT.
// Flipping either comparison makes this client disagree with the dispatcher, which
// is the entire failure this module exists to prevent.
test('starts_at exactly now is ACTIVE (starts_at <= NOW)', () => {
  expect(deriveStatus({ starts_at: '2026-08-09T12:00:00Z' }, NOW)).toBe('ACTIVE')
})

test('ends_at exactly now is ENDED (ends_at > NOW is false)', () => {
  expect(deriveStatus({ ends_at: '2026-08-09T12:00:00Z' }, NOW)).toBe('ENDED')
})

test('a future start is SCHEDULED', () => {
  expect(deriveStatus({ starts_at: '2026-08-09T18:00:00Z' }, NOW)).toBe('SCHEDULED')
})

test('a wholly past window is ENDED', () => {
  expect(
    deriveStatus({ starts_at: '2026-08-01T00:00:00Z', ends_at: '2026-08-02T00:00:00Z' }, NOW),
  ).toBe('ENDED')
})

test('an inverted window is ENDED, not SCHEDULED', () => {
  // The server accepts ends_at < starts_at and such a row can NEVER satisfy
  // ListActiveReservations, so it must read as dead rather than pending. ENDED is
  // checked first precisely to make this true.
  expect(
    deriveStatus({ starts_at: '2026-08-20T00:00:00Z', ends_at: '2026-08-10T00:00:00Z' }, NOW),
  ).toBe('ENDED')
})

test('ACTIVE agrees with the scheduler predicate on a matrix of windows', () => {
  // sqlSaysActive is a transcription of reservations.sql:21-22, written out
  // independently of deriveStatus' precedence structure. It only guards
  // ACTIVE-vs-not-ACTIVE - which is exactly the property shared with the
  // dispatcher; the ENDED-vs-SCHEDULED split is guarded by the cases above.
  function sqlSaysActive(r: { starts_at?: string; ends_at?: string }): boolean {
    const endsOk = r.ends_at === undefined || new Date(r.ends_at).getTime() > NOW.getTime()
    const startsOk = r.starts_at === undefined || new Date(r.starts_at).getTime() <= NOW.getTime()
    return endsOk && startsOk
  }
  const past = '2026-08-01T00:00:00Z'
  const exact = '2026-08-09T12:00:00Z'
  const future = '2026-09-01T00:00:00Z'
  const cases: { starts_at?: string; ends_at?: string }[] = []
  for (const s of [undefined, past, exact, future]) {
    for (const e of [undefined, past, exact, future]) {
      cases.push({ ...(s ? { starts_at: s } : {}), ...(e ? { ends_at: e } : {}) })
    }
  }
  for (const c of cases) {
    expect(deriveStatus(c, NOW) === 'ACTIVE').toBe(sqlSaysActive(c))
  }
  // The matrix must contain both outcomes, or the loop above proves nothing.
  expect(cases.filter(sqlSaysActive).length).toBeGreaterThan(0)
  expect(cases.filter((c) => !sqlSaysActive(c)).length).toBeGreaterThan(0)
})

test('tones map to the three Chip tones that exist', () => {
  expect(statusTone('ACTIVE')).toBe('accent')
  expect(statusTone('SCHEDULED')).toBe('warn')
  expect(statusTone('ENDED')).toBe('muted')
})
