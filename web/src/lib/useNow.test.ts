import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { useNow } from './useNow'

// Vitest's fake timers fake Date as well as setInterval by default, so
// vi.setSystemTime + advanceTimersByTime moves what `new Date()` returns.
beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

test('returns the current time and advances on each tick', () => {
  vi.setSystemTime(new Date('2026-08-09T00:00:00Z'))
  const { result } = renderHook(() => useNow(60_000))
  expect(result.current.toISOString()).toBe('2026-08-09T00:00:00.000Z')

  act(() => {
    vi.advanceTimersByTime(60_000)
  })
  expect(result.current.toISOString()).toBe('2026-08-09T00:01:00.000Z')

  act(() => {
    vi.advanceTimersByTime(120_000)
  })
  expect(result.current.toISOString()).toBe('2026-08-09T00:03:00.000Z')
})

test('does not tick before the interval elapses', () => {
  vi.setSystemTime(new Date('2026-08-09T00:00:00Z'))
  const { result } = renderHook(() => useNow(60_000))
  act(() => {
    vi.advanceTimersByTime(59_999)
  })
  expect(result.current.toISOString()).toBe('2026-08-09T00:00:00.000Z')
})

test('clears its interval on unmount', () => {
  vi.setSystemTime(new Date('2026-08-09T00:00:00Z'))
  const { result, unmount } = renderHook(() => useNow(60_000))
  const before = result.current
  unmount()
  act(() => {
    vi.advanceTimersByTime(600_000)
  })
  // Two independent proofs: the value is frozen, and no timer survives. The
  // second is what actually distinguishes a cleared interval from a stale
  // render snapshot.
  expect(result.current).toBe(before)
  expect(vi.getTimerCount()).toBe(0)
})
