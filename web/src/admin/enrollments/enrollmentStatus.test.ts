import { expect, test } from 'vitest'
import { deriveStatus, formatExpiryLabel, statusTone } from './enrollmentStatus'

const NOW = new Date('2026-08-09T12:00:00Z')

test('exactly at expires_at is EXPIRED', () => {
  expect(deriveStatus('2026-08-09T12:00:00Z', NOW)).toBe('EXPIRED')
})

test('already past expires_at is EXPIRED', () => {
  expect(deriveStatus('2026-08-09T11:59:59Z', NOW)).toBe('EXPIRED')
})

test('59m59s remaining is EXPIRING', () => {
  expect(deriveStatus('2026-08-09T12:59:59Z', NOW)).toBe('EXPIRING')
})

test('exactly 1h remaining is ACTIVE (the window is strictly under an hour)', () => {
  expect(deriveStatus('2026-08-09T13:00:00Z', NOW)).toBe('ACTIVE')
})

test('1h00m01s remaining is ACTIVE', () => {
  expect(deriveStatus('2026-08-09T13:00:01Z', NOW)).toBe('ACTIVE')
})

test('a nanosecond-precision RFC3339 timestamp parses (Go marshals time.Time this way)', () => {
  expect(deriveStatus('2026-08-10T12:00:00.123456789Z', NOW)).toBe('ACTIVE')
})

test('tones map to the three Chip tones that exist', () => {
  expect(statusTone('ACTIVE')).toBe('accent')
  expect(statusTone('EXPIRING')).toBe('warn')
  expect(statusTone('EXPIRED')).toBe('muted')
})

// EnrollmentsTable renders this instead of calling formatTimeUntil directly. The
// row's `now` comes from useNow(60_000): a local clock tick refreshed once a
// minute, not once a second. A seconds-precision label ("in 20s") computed from
// that `now` is only accurate at the instant of the tick - for up to 59 more
// real seconds the label stays frozen while the row's actual remaining time
// keeps falling, so a row can display "in 20s" / EXPIRING for nearly a minute
// after it has actually expired. Capping displayed precision at "in <1m" means
// the label can never promise more freshness than the 60s refresh actually
// delivers.
test('formatExpiryLabel collapses any sub-minute remainder to "in <1m" rather than showing stale seconds', () => {
  expect(formatExpiryLabel('2026-08-09T12:00:45Z', NOW)).toBe('in <1m')
  expect(formatExpiryLabel('2026-08-09T12:00:01Z', NOW)).toBe('in <1m')
})

test('formatExpiryLabel passes minutes and longer through unchanged - only seconds are coarse enough to go stale within a tick', () => {
  expect(formatExpiryLabel('2026-08-09T12:01:00Z', NOW)).toBe('in 1m')
  expect(formatExpiryLabel('2026-08-10T09:00:00Z', NOW)).toBe('in 21h')
})

test('formatExpiryLabel still reads expired at and past the boundary', () => {
  expect(formatExpiryLabel('2026-08-09T12:00:00Z', NOW)).toBe('expired')
  expect(formatExpiryLabel('2026-08-09T11:00:00Z', NOW)).toBe('expired')
})
