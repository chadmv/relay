import { describe, expect, test } from 'vitest'
import { livenessView, formatRelativeTime, specLine, labelChips } from './liveness'
import type { Worker } from './api'

describe('livenessView', () => {
  test('online is green, not dimmed', () => {
    expect(livenessView('online')).toEqual({
      label: 'ONLINE', dotClass: 'bg-ok', textClass: 'text-ok', dimClass: '',
    })
  })
  test('stale is amber, not dimmed', () => {
    expect(livenessView('stale')).toEqual({
      label: 'STALE', dotClass: 'bg-warn', textClass: 'text-warn', dimClass: '',
    })
  })
  test('disabled is grey and dimmed', () => {
    expect(livenessView('disabled')).toEqual({
      label: 'DISABLED', dotClass: 'bg-fg-mute', textClass: 'text-fg-mute', dimClass: 'opacity-70',
    })
  })
  test('offline is red and most dimmed', () => {
    expect(livenessView('offline')).toEqual({
      label: 'OFFLINE', dotClass: 'bg-err', textClass: 'text-err', dimClass: 'opacity-[0.55]',
    })
  })
})

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

function worker(over: Partial<Worker>): Worker {
  return {
    id: 'w', name: 'n', hostname: 'h', cpu_cores: 8, ram_gb: 64,
    gpu_count: 0, gpu_model: '', os: 'linux', max_slots: 4,
    labels: null, status: 'online', ...over,
  }
}

describe('specLine', () => {
  test('shows the GPU model when the worker has a GPU', () => {
    expect(specLine(worker({ gpu_count: 1, gpu_model: 'RTX 4090' }))).toBe('RTX 4090')
  })
  test('falls back to cpu/ram when there is no GPU', () => {
    expect(specLine(worker({ gpu_count: 0, cpu_cores: 16, ram_gb: 128 }))).toBe('16c · 128GB')
  })
})

describe('labelChips', () => {
  test('null labels yield no chips', () => {
    expect(labelChips(null)).toEqual([])
  })
  test('key=value pairs, bare key when value empty', () => {
    expect(labelChips({ pool: 'render', gpu: '' })).toEqual(['pool=render', 'gpu'])
  })
})
