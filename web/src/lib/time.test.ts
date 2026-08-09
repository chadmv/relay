import { describe, expect, test } from 'vitest'
import { formatRelativeTime, formatTimeUntil } from './time'

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
})
