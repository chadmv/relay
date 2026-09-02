import { act, renderHook } from '@testing-library/react'
import { expect, test } from 'vitest'
import { useCursorPager } from './useCursorPager'

// One act() per transition, never two calls inside one act(): result.current is
// re-read after each act, so a second call inside the same act would close over
// the pre-update render's state and silently test the wrong thing.

// A real array of the stated length, never an object with a `length` property: the
// hook reads items.length, and a fake that only carries `length` would let a
// mutation reading some other property survive.
function page(next_cursor: string, size: number) {
  return { next_cursor, items: Array.from({ length: size }, (_, i) => i) }
}

test('starts on the first page', () => {
  const { result } = renderHook(() => useCursorPager())
  expect(result.current.cursor).toBe('')
  expect(result.current.startOffset).toBe(0)
  expect(result.current.canPrev).toBe(false)
})

test('next advances the cursor and accumulates the real page size', () => {
  const { result } = renderHook(() => useCursorPager())
  act(() => {
    result.current.next(page('CUR1', 50))
  })
  expect(result.current.cursor).toBe('CUR1')
  expect(result.current.startOffset).toBe(50)
  expect(result.current.canPrev).toBe(true)
  act(() => {
    result.current.next(page('CUR2', 50))
  })
  expect(result.current.cursor).toBe('CUR2')
  expect(result.current.startOffset).toBe(100)
})

// The page carries the cursor and the rows together, so a caller cannot state a size
// that disagrees with the page it is leaving. 7 discriminates: an accumulation that
// adds a constant page size instead of items.length reports 50 here.
test("the offset advances by the page's own row count, not by a caller-supplied number", () => {
  const { result } = renderHook(() => useCursorPager())
  act(() => {
    result.current.next(page('CUR1', 7))
  })
  expect(result.current.cursor).toBe('CUR1')
  expect(result.current.startOffset).toBe(7)
})

test('prev walks back to the cursor of the page we came from', () => {
  const { result } = renderHook(() => useCursorPager())
  act(() => {
    result.current.next(page('CUR1', 50))
  })
  act(() => {
    result.current.next(page('CUR2', 50))
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
    result.current.next(page('CUR1', 50))
  })
  act(() => {
    result.current.next(page('', 13))
  })
  expect(result.current.cursor).toBe('CUR1')
  expect(result.current.startOffset).toBe(50)
})

test('next(undefined) is a no-op', () => {
  const { result } = renderHook(() => useCursorPager())
  act(() => {
    result.current.next(undefined)
  })
  expect(result.current.cursor).toBe('')
  expect(result.current.startOffset).toBe(0)
  expect(result.current.canPrev).toBe(false)
})

test('paging back off a partial last page restores the previous offset, not pageSize * depth', () => {
  const { result } = renderHook(() => useCursorPager())
  act(() => {
    result.current.next(page('CUR1', 50))
  })
  act(() => {
    result.current.next(page('CUR2', 13))
  })
  expect(result.current.startOffset).toBe(63)
  act(() => {
    result.current.prev()
  })
  expect(result.current.startOffset).toBe(50)
})

// Three pages with two DISTINCT partial sizes (13 then 50 then 7), so neither of the
// two wrong formulas below can coincide with the right answer by accident:
//   - `copy.length * 50` (stack depth times a fixed page size): with a two-page walk
//     of (50, 13) the first `prev` already diverges (50 vs the naive 1*50), but that
//     collision is with the SAME number this test also uses for pageSize, which is
//     exactly the coincidence that let a naive formula hide. Three distinct sizes
//     remove any pageSize value that could double as "the" pageSize.
//   - `startOffset - 50` (subtract a fixed page size from the running total instead of
//     popping the real offsets stack): on a two-page walk of (13, 50), the second
//     page's real size (50) happens to equal the constant being subtracted, so
//     restoring 63 - 50 = 13 matches the correct answer BY COINCIDENCE. A third page
//     of a third size breaks that coincidence.
test('paging back through three partial pages restores each real offset, not a fixed-page-size guess', () => {
  const { result } = renderHook(() => useCursorPager())
  act(() => {
    result.current.next(page('CUR1', 13))
  })
  act(() => {
    result.current.next(page('CUR2', 50))
  })
  act(() => {
    result.current.next(page('CUR3', 7))
  })
  expect(result.current.startOffset).toBe(70)
  act(() => {
    result.current.prev()
  })
  expect(result.current.startOffset).toBe(63)
  act(() => {
    result.current.prev()
  })
  expect(result.current.startOffset).toBe(13)
})

test('resetPaging returns to the first page', () => {
  let renders = 0
  const { result } = renderHook(() => {
    renders++
    return useCursorPager()
  })
  act(() => {
    result.current.next(page('CUR1', 50))
  })
  act(() => {
    result.current.next(page('CUR2', 50))
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
