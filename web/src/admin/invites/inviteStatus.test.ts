import { expect, test } from 'vitest'
import { deriveStatus, formatExpiryLabel, statusTone } from './inviteStatus'

const NOW = new Date('2026-08-09T12:00:00Z')

test('an unredeemed invite with a day left is ACTIVE', () => {
  expect(deriveStatus({ expires_at: '2026-08-10T12:00:00Z' }, NOW)).toBe('ACTIVE')
})

test('an unredeemed invite with 30m left is EXPIRING', () => {
  expect(deriveStatus({ expires_at: '2026-08-09T12:30:00Z' }, NOW)).toBe('EXPIRING')
})

test('an unredeemed invite past its expiry is EXPIRED', () => {
  expect(deriveStatus({ expires_at: '2026-08-09T11:00:00Z' }, NOW)).toBe('EXPIRED')
})

test('an invite with used_at set is REDEEMED', () => {
  expect(
    deriveStatus({ expires_at: '2026-08-10T12:00:00Z', used_at: '2026-08-09T10:00:00Z' }, NOW),
  ).toBe('REDEEMED')
})

// THE discriminating test for the ordering. An implementation that checks expiry
// before redemption passes every other test in this file and fails only this one.
// Redemption is terminal and one-way - MarkInviteUsed is the only writer and
// carries `AND used_at IS NULL` (internal/store/query/invites.sql:9-12), called
// once from registration (internal/api/auth.go:147-158) - so expiry of an
// already-spent credential is a non-event. README.md:1300-1301 documents this
// precedence as the shipped contract.
test('a REDEEMED invite that is ALSO past its expiry reads REDEEMED, never EXPIRED', () => {
  expect(
    deriveStatus({ expires_at: '2026-08-09T11:00:00Z', used_at: '2026-08-09T10:00:00Z' }, NOW),
  ).toBe('REDEEMED')
})

test('exactly at expires_at is EXPIRED', () => {
  expect(deriveStatus({ expires_at: '2026-08-09T12:00:00Z' }, NOW)).toBe('EXPIRED')
})

test('59m59s remaining is EXPIRING', () => {
  expect(deriveStatus({ expires_at: '2026-08-09T12:59:59Z' }, NOW)).toBe('EXPIRING')
})

test('exactly 1h remaining is ACTIVE (the window is strictly under an hour)', () => {
  expect(deriveStatus({ expires_at: '2026-08-09T13:00:00Z' }, NOW)).toBe('ACTIVE')
})

test('1h00m01s remaining is ACTIVE', () => {
  expect(deriveStatus({ expires_at: '2026-08-09T13:00:01Z' }, NOW)).toBe('ACTIVE')
})

test('a nanosecond-precision RFC3339 timestamp parses (Go marshals time.Time this way)', () => {
  expect(deriveStatus({ expires_at: '2026-08-10T12:00:00.123456789Z' }, NOW)).toBe('ACTIVE')
})

test('tones map all four states, including the Chip tone added for this tab', () => {
  expect(statusTone('ACTIVE')).toBe('accent')
  expect(statusTone('EXPIRING')).toBe('warn')
  expect(statusTone('EXPIRED')).toBe('err')
  expect(statusTone('REDEEMED')).toBe('muted')
})

test('formatExpiryLabel collapses any sub-minute remainder to "in <1m"', () => {
  expect(formatExpiryLabel('2026-08-09T12:00:45Z', NOW)).toBe('in <1m')
  expect(formatExpiryLabel('2026-08-09T12:00:01Z', NOW)).toBe('in <1m')
})

test('formatExpiryLabel passes minutes and longer through unchanged', () => {
  expect(formatExpiryLabel('2026-08-09T12:01:00Z', NOW)).toBe('in 1m')
  expect(formatExpiryLabel('2026-08-10T09:00:00Z', NOW)).toBe('in 21h')
})

test('formatExpiryLabel still reads expired at and past the boundary', () => {
  expect(formatExpiryLabel('2026-08-09T12:00:00Z', NOW)).toBe('expired')
  expect(formatExpiryLabel('2026-08-09T11:00:00Z', NOW)).toBe('expired')
})
