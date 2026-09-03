import { renderHook } from '@testing-library/react'
import { expect, test, vi } from 'vitest'
import { useDebouncedPagingGuard } from './useDebouncedPagingGuard'

function pager() {
  return { resetPaging: vi.fn() }
}

test('unsettled is false when raw already matches the debounced value', () => {
  const p = pager()
  const { result } = renderHook(() => useDebouncedPagingGuard('etl', 'etl', p))
  expect(result.current).toBe(false)
})

test('unsettled is true while the raw input has outrun the debounced value', () => {
  const p = pager()
  const { result, rerender } = renderHook(
    ({ raw, debounced }) => useDebouncedPagingGuard(raw, debounced, p),
    { initialProps: { raw: '', debounced: '' } },
  )
  rerender({ raw: 'e', debounced: '' })
  expect(result.current).toBe(true)
})

test('unsettled clears once the debounced value catches up', () => {
  const p = pager()
  const { result, rerender } = renderHook(
    ({ raw, debounced }) => useDebouncedPagingGuard(raw, debounced, p),
    { initialProps: { raw: 'e', debounced: '' } },
  )
  expect(result.current).toBe(true)
  rerender({ raw: 'e', debounced: 'e' })
  expect(result.current).toBe(false)
})

test('resets the pager when the debounced value changes, not on the initial mount', () => {
  const p = pager()
  const { rerender } = renderHook(
    ({ raw, debounced }) => useDebouncedPagingGuard(raw, debounced, p),
    { initialProps: { raw: '', debounced: '' } },
  )
  expect(p.resetPaging).not.toHaveBeenCalled()

  rerender({ raw: 'e', debounced: '' })
  // The raw-only change is what `unsettled` tracks; it must not itself reset
  // paging - that would put the reset on the vulnerable side of the race
  // instead of after the debounce actually lands.
  expect(p.resetPaging).not.toHaveBeenCalled()

  rerender({ raw: 'e', debounced: 'e' })
  expect(p.resetPaging).toHaveBeenCalledTimes(1)
})

test('does not reset again on a render where the debounced value is unchanged', () => {
  const p = pager()
  const { rerender } = renderHook(
    ({ raw, debounced }) => useDebouncedPagingGuard(raw, debounced, p),
    { initialProps: { raw: 'e', debounced: 'e' } },
  )
  rerender({ raw: 'et', debounced: 'e' })
  rerender({ raw: 'e', debounced: 'e' })
  expect(p.resetPaging).not.toHaveBeenCalled()
})
