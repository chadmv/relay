import { expect, test } from 'vitest'
import { deriveStatus, statusTone } from './enrollmentStatus'

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
