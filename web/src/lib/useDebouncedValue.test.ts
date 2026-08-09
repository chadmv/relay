import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { useDebouncedValue } from './useDebouncedValue'

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

test('returns the initial value immediately', () => {
  const { result } = renderHook(() => useDebouncedValue('a', 300))
  expect(result.current).toBe('a')
})

test('holds the old value until the delay elapses', () => {
  const { result, rerender } = renderHook(({ v }) => useDebouncedValue(v, 300), {
    initialProps: { v: 'a' },
  })
  rerender({ v: 'ab' })
  act(() => {
    vi.advanceTimersByTime(299)
  })
  expect(result.current).toBe('a')
  act(() => {
    vi.advanceTimersByTime(1)
  })
  expect(result.current).toBe('ab')
})

test('a burst of changes emits only the last value', () => {
  const { result, rerender } = renderHook(({ v }) => useDebouncedValue(v, 300), {
    initialProps: { v: '' },
  })
  rerender({ v: 'a' })
  act(() => {
    vi.advanceTimersByTime(100)
  })
  rerender({ v: 'ab' })
  act(() => {
    vi.advanceTimersByTime(100)
  })
  rerender({ v: 'abc' })
  // Only 200ms of the 300ms window has elapsed since 'a', and the timer restarted
  // twice, so nothing has been emitted yet.
  expect(result.current).toBe('')
  act(() => {
    vi.advanceTimersByTime(300)
  })
  expect(result.current).toBe('abc')
})
