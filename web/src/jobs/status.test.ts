import { expect, test } from 'vitest'
import { statusColor, progressPct, formatDuration, formatStarted } from './status'

test('statusColor maps each bucket', () => {
  expect(statusColor('done').dot).toBe('bg-ok')
  expect(statusColor('running').dot).toBe('bg-accent')
  expect(statusColor('dispatched').dot).toBe('bg-accent')
  expect(statusColor('queued').dot).toBe('bg-warn')
  expect(statusColor('pending').dot).toBe('bg-warn')
  expect(statusColor('failed').dot).toBe('bg-err')
  expect(statusColor('timed_out').dot).toBe('bg-err')
  expect(statusColor('cancelled').dot).toBe('bg-fg-mute')
})

test('progressPct rounds done/total, 0 when no tasks', () => {
  expect(progressPct(48, 64)).toBe(75)
  expect(progressPct(0, 0)).toBe(0)
  expect(progressPct(undefined, undefined)).toBe(0)
})

test('formatDuration uses finished when present, now otherwise', () => {
  const started = '2026-06-05T12:00:00Z'
  const finished = '2026-06-05T12:14:00Z'
  expect(formatDuration(started, finished)).toBe('14m')
  const now = new Date('2026-06-05T14:14:00Z').getTime()
  expect(formatDuration(started, undefined, now)).toBe('2h 14m')
})

test('formatDuration returns dash when not started', () => {
  expect(formatDuration(undefined, undefined)).toBe('-')
})

test('formatStarted returns dash when null', () => {
  expect(formatStarted(undefined)).toBe('-')
  expect(formatStarted('2026-06-05T12:00:00Z')).not.toBe('-')
})
