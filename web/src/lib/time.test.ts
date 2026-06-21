import { describe, expect, test } from 'vitest'
import { formatRelativeTime } from './time'

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
