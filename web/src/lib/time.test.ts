import { describe, expect, test } from 'vitest'
import { formatDateTime, formatRelativeTime, formatTimeUntil } from './time'

describe('formatRelativeTime', () => {
  const now = new Date('2026-06-03T12:00:00Z')

  test('seconds', () => {
    expect(formatRelativeTime('2026-06-03T11:59:48Z', now)).toBe('12s ago')
  })
  test('minutes', () => {
    expect(formatRelativeTime('2026-06-03T11:55:00Z', now)).toBe('5m ago')
  })
  test('hours', () => {
    expect(formatRelativeTime('2026-06-03T09:00:00Z', now)).toBe('3h ago')
  })
  test('days', () => {
    expect(formatRelativeTime('2026-06-01T12:00:00Z', now)).toBe('2d ago')
  })
  test('future clamps to 0s', () => {
    expect(formatRelativeTime('2026-06-03T12:00:30Z', now)).toBe('0s ago')
  })
})

describe('formatTimeUntil', () => {
  const now = new Date('2026-08-09T12:00:00Z')

  test('seconds', () => {
    expect(formatTimeUntil('2026-08-09T12:00:45Z', now)).toBe('in 45s')
  })
  test('minutes', () => {
    expect(formatTimeUntil('2026-08-09T12:42:00Z', now)).toBe('in 42m')
  })
  test('hours', () => {
    expect(formatTimeUntil('2026-08-10T09:42:00Z', now)).toBe('in 21h')
  })
  test('days', () => {
    expect(formatTimeUntil('2026-08-16T12:00:00Z', now)).toBe('in 7d')
  })
  test('the instant of expiry, and any past instant, read as expired', () => {
    expect(formatTimeUntil('2026-08-09T12:00:00Z', now)).toBe('expired')
    expect(formatTimeUntil('2026-08-09T11:00:00Z', now)).toBe('expired')
  })

  // Positive control for WHY this function exists: formatRelativeTime clamps the
  // future to zero (time.ts:2) and appends "ago", so reusing it for an expiry
  // would render every future expiry as "0s ago". If this assertion ever fails,
  // formatRelativeTime changed and formatTimeUntil may be redundant.
  test('formatRelativeTime cannot do this - it renders the same future as 0s ago', () => {
    expect(formatRelativeTime('2026-08-10T09:42:00Z', now)).toBe('0s ago')
  })

  // enrollmentStatus.deriveStatus expires a row exactly at remaining <= 0ms
  // (enrollmentStatus.ts:20-21). The old Math.round-based implementation here
  // rounded any sub-500ms remainder DOWN to 0 whole seconds and then treated
  // secs <= 0 as expired, so a row with genuinely positive time left (e.g.
  // 300ms) read "expired" while deriveStatus still called it EXPIRING - the two
  // derivations disagreeing in the last half-second despite both operating on
  // the same instant.
  test('a still-positive sub-second remainder is not reported as expired (matches deriveStatus, which only expires at <= 0ms)', () => {
    const soon = new Date(now.getTime() + 300).toISOString() // 300ms remaining
    expect(formatTimeUntil(soon, now)).toBe('in 0s')
  })

  // The other side of the same boundary: the exact expiry instant, and anything
  // past it, must still read "expired" - unchanged behavior, asserted again here
  // pinned to raw millisecond deltas rather than whole seconds, so a future edit
  // to the rounding strategy cannot silently widen the boundary back open.
  test('a 1ms-past instant still reads expired', () => {
    const justPast = new Date(now.getTime() - 1).toISOString()
    expect(formatTimeUntil(justPast, now)).toBe('expired')
  })
})

describe('formatDateTime', () => {
  // TZ-INDEPENDENT BY CONSTRUCTION. The input is built from LOCAL Date components
  // and the expected string spells out those same components, so this holds in
  // every runner timezone. A test that asserted a literal against a 'Z' input
  // would pass only in a UTC CI, which is worse than no test at all.
  test('renders an instant as local YYYY-MM-DD HH:MM', () => {
    const local = new Date(2026, 7, 9, 14, 5) // 2026-08-09 14:05 LOCAL
    expect(formatDateTime(local.toISOString())).toBe('2026-08-09 14:05')
  })

  test('zero-pads month, day, hour and minute', () => {
    const local = new Date(2026, 0, 3, 9, 7) // 2026-01-03 09:07 LOCAL
    expect(formatDateTime(local.toISOString())).toBe('2026-01-03 09:07')
  })

  test('midnight is 00:00, not 24:00 and not blank', () => {
    const local = new Date(2026, 11, 31, 0, 0)
    expect(formatDateTime(local.toISOString())).toBe('2026-12-31 00:00')
  })

  test('the shape is fixed, so it is not toLocaleString output', () => {
    // toLocaleString would emit '8/9/2026, 2:05:00 PM' under en-US and something
    // else entirely under another ICU locale. This asserts the invariant shape.
    const local = new Date(2026, 7, 9, 14, 5)
    expect(formatDateTime(local.toISOString())).toMatch(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/)
  })

  test('positive control: off UTC the local rendering really does differ from the ISO text', () => {
    // Proves the first test is exercising a conversion rather than an accidental
    // pass-through. The control is on the TEST DATA, not on the function: for this
    // same instant the raw ISO text only reads '2026-01-15 09:30' at UTC+0.
    const local = new Date(2026, 0, 15, 9, 30)
    expect(formatDateTime(local.toISOString())).toBe('2026-01-15 09:30')
    const isoText = local.toISOString().slice(0, 16).replace('T', ' ')
    if (local.getTimezoneOffset() !== 0) {
      expect(isoText).not.toBe('2026-01-15 09:30')
    }
  })

  test('a nanosecond-precision RFC3339 timestamp parses (Go marshals time.Time this way)', () => {
    const local = new Date(2026, 7, 9, 14, 5)
    const withNanos = local.toISOString().replace('.000Z', '.123456789Z')
    expect(formatDateTime(withNanos)).toBe('2026-08-09 14:05')
  })

  // L5 (review 2026-08-09): not reachable from the Go server today (RFC3339
  // timestamps only), but a garbage value must degrade to a dash rather than the
  // literal 'NaN-NaN-NaN NaN:NaN' rendering into a table cell.
  test('a malformed timestamp renders a dash, never NaN garbage', () => {
    expect(formatDateTime('not-a-real-timestamp')).toBe('-')
  })
})
