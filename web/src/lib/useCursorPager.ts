import { useState } from 'react'

// One page-walk over a cursor-paginated list endpoint.
//
// The server returns only `next_cursor`, never a previous one, so walking BACK means
// remembering where we came from. `stack` holds the cursors of the pages we paged
// forward FROM, so `stack.length` is the current page index and `[]` is the first
// page. `offsets` holds the real row offset each of those pages started at, and
// `startOffset` is the rows accumulated before the CURRENT page. startOffset grows by
// the ACTUAL page size on each forward step rather than by a fixed limit, so a partial
// final page keeps the footer's absolute range honest - a partial final page has
// shipped a wrong footer range before:
//   docs/backlog/closed/bug-2026-06-05-jobs-pagination-footer-absolute-range.md
//   docs/backlog/closed/bug-2026-06-21-schedules-pagination-footer-absolute-range.md
//
// next/prev/resetPaging use plain setters, NOT functional updaters: cursor, stack,
// offsets and startOffset are all read from the current render and React batches the
// updates in one event. Mixing a functional stack updater with plain offset setters
// would desync the two stacks under StrictMode. Do not "tidy" it into a single
// useState holding one object with one functional updater: that changes the update
// mechanics of every surface that uses this hook, and the current shape has no known
// defect.

/**
 * One page of a cursor-paginated list response, as the pager needs to see it.
 *
 * `next_cursor` is REQUIRED, not optional, so a response type that drops or renames it
 * fails to compile at the call site rather than reading `undefined`.
 * The pager reads `items` only for its length and never mutates it.
 */
export interface CursorPage {
  next_cursor: string
  items: readonly unknown[]
}

export interface CursorPager {
  /** Cursor of the current page. '' is the first page. */
  cursor: string
  /** Rows accumulated before the current page. Feed to computePageRange. */
  startOffset: number
  /**
   * True when there is a page to go back to. Consumers get this boolean rather
   * than the stack itself: a consumer holding the array could mutate it
   * (`stack.pop()` on a React state array is a live footgun) and desync `offsets`
   * from `cursor` behind the hook's back. Value out, mutation only through these
   * methods.
   */
  canPrev: boolean
  /**
   * Advance one page, given the page being LEFT. A page whose `next_cursor` is falsy
   * (there is no further page) is a no-op, and so is `undefined`. The parameter
   * admits `undefined` on purpose: call sites read it off a possibly-undefined
   * query result, and the union makes tsc ENFORCE the falsy guard - delete the guard
   * and `page.next_cursor` stops compiling, so it is a compile error rather than an
   * untested regression. The offset advances by `page.items.length`, the ACTUAL rows
   * on the page being left, never a request limit.
   */
  next: (page: CursorPage | undefined) => void
  /** Go back one page. A no-op on the first page. */
  prev: () => void
  /**
   * Return to the first page. Consumers MUST call this whenever the query's sort
   * key or its filters change: the server 400s a cursor issued under a different
   * sort ("cursor sort key does not match requested sort", internal/api/pagination.go).
   * The hook deliberately does not watch a sort argument - a surface can reset on a
   * sort, on a status filter, on include_archived or on a debounced search box, and a
   * single `sort` dependency does not model that.
   */
  resetPaging: () => void
}

// There is deliberately no `canNext`. Whether a further page exists is a fact about
// the query result, not about the pager, so moving it in would make this hook depend
// on each surface's response shape.
export function useCursorPager(): CursorPager {
  const [cursor, setCursor] = useState('')
  const [stack, setStack] = useState<string[]>([])
  const [startOffset, setStartOffset] = useState(0)
  const [offsets, setOffsets] = useState<number[]>([])

  function next(page: CursorPage | undefined) {
    if (!page?.next_cursor) return
    setStack([...stack, cursor])
    setCursor(page.next_cursor)
    setOffsets([...offsets, startOffset])
    setStartOffset(startOffset + page.items.length)
  }

  function prev() {
    if (stack.length === 0) return
    const copy = [...stack]
    const back = copy.pop() ?? ''
    setStack(copy)
    setCursor(back)
    const offsetsCopy = [...offsets]
    const prevOffset = offsetsCopy.pop() ?? 0
    setOffsets(offsetsCopy)
    setStartOffset(prevOffset)
  }

  function resetPaging() {
    setCursor('')
    setStack([])
    setStartOffset(0)
    // Clearing offsets is not observable: offsets is popped only while stack is
    // non-empty, and next pushes exactly one offset per stack entry, so a stale
    // prefix is dead weight the pops never reach. Kept so the state stays honest and
    // a future reader is not left wondering which piece was left behind on purpose.
    setOffsets([])
  }

  // Not memoized: a fresh identity per render is intentional; memoizing would change
  // effect dependencies for a consumer that captures these.
  return { cursor, startOffset, canPrev: stack.length > 0, next, prev, resetPaging }
}
