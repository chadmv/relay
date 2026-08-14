import { useState } from 'react'

// One page-walk over a cursor-paginated list endpoint. Seven list surfaces used to
// carry a copy of this each (JobsPage, WorkersPage, SchedulesPage, and the four
// admin tabs); this is the single owner.
//
// The server returns only `next_cursor`, never a previous one, so walking BACK means
// remembering where we came from. `stack` holds the cursors of the pages we paged
// forward FROM, so `stack.length` is the current page index and `[]` is the first
// page. `offsets` holds the real row offset each of those pages started at, and
// `startOffset` is the rows accumulated before the CURRENT page. startOffset grows by
// the ACTUAL page size on each forward step rather than by a fixed limit, so a partial
// final page keeps the footer's absolute range honest - that has already been shipped
// as a bug twice, on jobs and on schedules:
//   docs/backlog/closed/bug-2026-06-05-jobs-pagination-footer-absolute-range.md
//   docs/backlog/closed/bug-2026-06-21-schedules-pagination-footer-absolute-range.md
//
// next/prev/resetPaging use plain setters, NOT functional updaters: cursor, stack,
// offsets and startOffset are all read from the current render and React batches the
// updates in one event. Mixing a functional stack updater with plain offset setters
// would desync the two stacks under StrictMode. This is the merged form of the warning
// that used to sit separately in JobsPage, SchedulesPage and UsersTab. Do not "tidy"
// it into a single useState holding one object with one functional updater: that
// changes the update mechanics of seven shipped surfaces, and the current shape has
// no known defect.
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
   * Advance one page. `nextCursor` is the response's `next_cursor`; a falsy value
   * (there is no further page) is a no-op. The parameter admits `undefined` on
   * purpose: every call site reads it off a possibly-undefined query result, and
   * the union makes tsc ENFORCE the falsy guard - deleting it is a compile error,
   * not merely an untested regression. `pageSize` is the ACTUAL number of rows on
   * the page being left, never the request limit.
   */
  next: (nextCursor: string | undefined, pageSize: number) => void
  /** Go back one page. A no-op on the first page. */
  prev: () => void
  /**
   * Return to the first page. Consumers MUST call this whenever the query's sort
   * key or its filters change: the server 400s a cursor issued under a different
   * sort ("cursor sort key does not match requested sort", internal/api/pagination.go).
   * The hook deliberately does not watch a sort argument - the surfaces reset on
   * six different trigger conditions (sort, status filter, include_archived, a
   * debounced email), and a single `sort` dependency does not model that.
   */
  resetPaging: () => void
}

// There is deliberately no `canNext`. Every surface computes it as
// `!data?.next_cursor`, which is a fact about the query result, not about the pager,
// and moving it in would make this hook depend on seven different response shapes.
export function useCursorPager(): CursorPager {
  const [cursor, setCursor] = useState('')
  const [stack, setStack] = useState<string[]>([])
  const [startOffset, setStartOffset] = useState(0)
  const [offsets, setOffsets] = useState<number[]>([])

  function next(nextCursor: string | undefined, pageSize: number) {
    if (!nextCursor) return
    setStack([...stack, cursor])
    setCursor(nextCursor)
    setOffsets([...offsets, startOffset])
    setStartOffset(startOffset + pageSize)
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
    // Clearing offsets is NOT observable and no test covers it: offsets is popped
    // only while stack is non-empty, and next pushes exactly one offset per stack
    // entry, so a stale prefix is dead weight the pops never reach. Kept anyway,
    // byte-for-byte with the seven originals, so the state stays honest and a future
    // reader is not left wondering which of the four was left behind on purpose.
    setOffsets([])
  }

  // Not wrapped in useCallback on purpose. All seven surfaces declared these as plain
  // function declarations in the component body, so a fresh identity per render IS the
  // shipped behaviour. Memoizing here would be a change, not a cleanup.
  return { cursor, startOffset, canPrev: stack.length > 0, next, prev, resetPaging }
}
