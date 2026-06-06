import { expect, test } from 'vitest'
import { formatRelativeTime, nextRunDisplay, shortId } from './format'

const now = new Date('2026-06-05T12:00:00Z')

test('formatRelativeTime renders past times as "ago"', () => {
  expect(formatRelativeTime('2026-06-05T11:55:00Z', now)).toBe('5m ago')
  expect(formatRelativeTime('2026-06-05T11:59:30Z', now)).toBe('30s ago')
  expect(formatRelativeTime('2026-06-05T10:00:00Z', now)).toBe('2h ago')
})

test('nextRunDisplay renders future times as "in"', () => {
  expect(nextRunDisplay('2026-06-05T12:07:00Z', now)).toBe('in 7m')
  expect(nextRunDisplay('2026-06-05T12:00:30Z', now)).toBe('in 30s')
  expect(nextRunDisplay('2026-06-05T14:00:00Z', now)).toBe('in 2h')
})

test('nextRunDisplay renders past/now as "due"', () => {
  expect(nextRunDisplay('2026-06-05T11:59:00Z', now)).toBe('due')
})

test('shortId takes the first 8 chars', () => {
  expect(shortId('abcdef12-3456-7890-abcd-ef1234567890')).toBe('abcdef12')
  expect(shortId('')).toBe('-')
  expect(shortId(undefined)).toBe('-')
})
