import { act, renderHook } from '@testing-library/react'
import { expect, test } from 'vitest'
import { useCursorPager } from './useCursorPager'

// One act() per transition, never two calls inside one act(): result.current is
// re-read after each act, so a second call inside the same act would close over
// the pre-update render's state and silently test the wrong thing.

test('starts on the first page', () => {
  const { result } = renderHook(() => useCursorPager())
  expect(result.current.cursor).toBe('')
  expect(result.current.startOffset).toBe(0)
  expect(result.current.canPrev).toBe(false)
})

test('next advances the cursor and accumulates the real page size', () => {
  const { result } = renderHook(() => useCursorPager())
  act(() => {
    result.current.next('CUR1', 50)
  })
  expect(result.current.cursor).toBe('CUR1')
  expect(result.current.startOffset).toBe(50)
  expect(result.current.canPrev).toBe(true)
  act(() => {
    result.current.next('CUR2', 50)
  })
  expect(result.current.cursor).toBe('CUR2')
  expect(result.current.startOffset).toBe(100)
})

test('prev walks back to the cursor of the page we came from', () => {
  const { result } = renderHook(() => useCursorPager())
  act(() => {
    result.current.next('CUR1', 50)
  })
  act(() => {
    result.current.next('CUR2', 50)
  })
  act(() => {
    result.current.prev()
  })
  expect(result.current.cursor).toBe('CUR1')
  expect(result.current.startOffset).toBe(50)
  expect(result.current.canPrev).toBe(true)
  act(() => {
    result.current.prev()
  })
  expect(result.current.cursor).toBe('')
  expect(result.current.startOffset).toBe(0)
  expect(result.current.canPrev).toBe(false)
})

test('a page with no next_cursor is a no-op', () => {
  const { result } = renderHook(() => useCursorPager())
  act(() => {
    result.current.next('CUR1', 50)
  })
  act(() => {
    result.current.next('', 13)
  })
  expect(result.current.cursor).toBe('CUR1')
  expect(result.current.startOffset).toBe(50)
})

test('next(undefined, n) is a no-op', () => {
  const { result } = renderHook(() => useCursorPager())
  act(() => {
    result.current.next(undefined, 50)
  })
  expect(result.current.cursor).toBe('')
  expect(result.current.startOffset).toBe(0)
  expect(result.current.canPrev).toBe(false)
})

test('paging back off a partial last page restores the previous offset, not pageSize * depth', () => {
  const { result } = renderHook(() => useCursorPager())
  act(() => {
    result.current.next('CUR1', 50)
  })
  act(() => {
    result.current.next('CUR2', 13)
  })
  expect(result.current.startOffset).toBe(63)
  act(() => {
    result.current.prev()
  })
  expect(result.current.startOffset).toBe(50)
})

test('resetPaging returns to the first page', () => {
  let renders = 0
  const { result } = renderHook(() => {
    renders++
    return useCursorPager()
  })
  act(() => {
    result.current.next('CUR1', 50)
  })
  act(() => {
    result.current.next('CUR2', 50)
  })
  act(() => {
    result.current.resetPaging()
  })
  expect(result.current.cursor).toBe('')
  expect(result.current.startOffset).toBe(0)
  expect(result.current.canPrev).toBe(false)
  // prev on the first page is a no-op, and the guard returns BEFORE touching
  // state: without it the pops fall back to ''/0 and produce identical values,
  // so the render count is the only observable difference.
  const before = renders
  act(() => {
    result.current.prev()
  })
  expect(renders).toBe(before)
  expect(result.current.cursor).toBe('')
  expect(result.current.startOffset).toBe(0)
  expect(result.current.canPrev).toBe(false)
})
