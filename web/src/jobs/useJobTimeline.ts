import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { useRef } from 'react'
import { listJobsInWindow, type Job } from './api'
import {
  ANCHOR_STEP_MS,
  TIMELINE_MAX_PAGES,
  windowBounds,
  type TimelineWindow,
} from './timelineWindow'

// The view's whole render input, the way LaneState is for the lanes. The view gets
// this instead of a UseQueryResult so its tests can state a condition directly
// instead of driving a query to reach it.
export interface TimelineState {
  jobs: readonly Job[]
  /** From the LAST response fetched, so the caption's denominator is the freshest. */
  total: number
  truncated: boolean
  sinceIso: string
  untilIso: string
  /**
   * The window sinceIso/untilIso and jobs actually belong to. This is the
   * caller's live picker selection whenever a fetch under it has landed, but
   * during a failed refresh under a NEWLY chosen window it stays at whichever
   * window the still-displayed stale rows came from - the caller's own
   * picker selection has already moved on, and labelling stale rows with the
   * new selection would print a caption and a set of axis dates that describe
   * two different windows at once.
   */
  window: TimelineWindow
  isLoading: boolean
  isFetching: boolean
  error: Error | null
  refetch: () => void
}

export interface TimelineWalk {
  jobs: Job[]
  total: number
  truncated: boolean
  /** The bounds THIS walk actually queried, so the caption never describes a
   * different window than the rows it is looking at. */
  sinceIso: string
  untilIso: string
}

/**
 * Walks the window's pages in sequence to TIMELINE_MAX_PAGES.
 *
 * SEQUENTIAL BY CONSTRUCTION. Page N+1's cursor is only knowable from page N's
 * response, so the set of requests cannot be enumerated before the walk runs and
 * no static array of queries can express it.
 *
 * Truncation comes from the CURSOR, never from arithmetic. `drawn < total` is
 * true on a window that drained, because jobs are created while the walk runs and
 * `total` grows under it - that comparison raises a false banner on a complete
 * window. `total` is used only for the number the banner prints.
 *
 * The signal is consumed on every page AND checked before the next is released.
 * That is the frontend form of ending a generation before releasing its resource:
 * a window change, a debounce landing, a My-jobs click or a view switch each mint
 * a new key, and the walk being replaced must stop before the new one competes
 * with it for the browser's connections.
 */
export async function walkJobWindow(
  args: { sinceIso: string; untilIso: string; q: string; mine: boolean },
  signal: AbortSignal,
): Promise<TimelineWalk> {
  const jobs: Job[] = []
  let cursor = ''
  let total = 0
  let pages = 0

  for (;;) {
    const page = await listJobsInWindow(args.sinceIso, args.untilIso, cursor, args.q, args.mine, signal)
    pages++
    jobs.push(...page.items)
    total = page.total
    cursor = page.next_cursor
    if (cursor === '' || pages >= TIMELINE_MAX_PAGES) break
    // Checked between pages as well as passed to the fetch: an abort that lands
    // while nothing is in flight has no request to reject. Throwing rather than
    // returning a partial keeps this consistent with the in-flight abort path,
    // which rejects; the abandoned key is inactive and gc'd shortly after.
    if (signal.aborted) throw new Error('timeline walk cancelled')
  }

  return {
    jobs,
    total,
    truncated: pages >= TIMELINE_MAX_PAGES && cursor !== '',
    sinceIso: args.sinceIso,
    untilIso: args.untilIso,
  }
}

/**
 * One window of jobs, drawn from up to TIMELINE_MAX_PAGES pages.
 *
 * THE KEY IS STABLE per window and filters - it does not carry since/until, so a
 * refresh does not mint a new query. The anchor is instead computed ONCE, inside
 * the queryFn, at the moment each fetch actually starts (`windowBounds(w,
 * Date.now())`), and travels back out on the result (TimelineWalk.sinceIso/
 * untilIso) so the caption always describes the rows it is looking at rather than
 * a bound recomputed separately in render.
 *
 * A per-tick key was tried first and does not work: a walk slower than one
 * ANCHOR_STEP_MS never completes, because the tick changes the key before the
 * walk can finish, the query goes inactive, its consumed signal aborts, and the
 * next tick starts the walk over from page 1 - forever, for any walk slower than
 * a tick. `refetchInterval: ANCHOR_STEP_MS` is what drives the refresh instead:
 * TanStack does not start an interval-triggered refetch while one is already in
 * flight for the same key, so a slow walk is never interrupted by its own tick.
 * useJobTimeline.test.tsx's 'a walk slower than one anchor tick still completes'
 * is the regression guard.
 *
 * keepPreviousData covers a window or filter change WHILE its new key's fetch
 * is still pending: TanStack fills query.data from the previous key so the view
 * does not blank while waiting. It does NOT cover the fetch then FAILING - once
 * a query definitively resolves to an error, TanStack drops the placeholder and
 * query.data goes back to undefined (measured: JobsPage.timeline.test.tsx's
 * 'a window change whose new key fails' went red with only keepPreviousData in
 * place, rows and bounds both gone, before this ref was added). lastSuccess is
 * this hook's own memory of the last walk that actually returned data, kept
 * across both the pending gap and any number of failed refreshes after it, so a
 * failed fetch degrades to stale rows rather than to nothing.
 */
export function useJobTimeline(
  enabled: boolean,
  w: TimelineWindow,
  q: string,
  mine: boolean,
): TimelineState {
  const query = useQuery({
    queryKey: ['job-timeline', w, q, mine],
    queryFn: ({ signal }) => {
      const { sinceIso, untilIso } = windowBounds(w, Date.now())
      return walkJobWindow({ sinceIso, untilIso, q, mine }, signal)
    },
    enabled,
    refetchInterval: ANCHOR_STEP_MS,
    placeholderData: keepPreviousData,
  })

  const lastSuccess = useRef<TimelineWalk | null>(null)
  const lastSuccessWindow = useRef<TimelineWindow | null>(null)
  // isPlaceholderData excludes the keepPreviousData borrow: while a NEW key is
  // pending, query.data is truthy but belongs to the OLD key, so recording `w`
  // (the NEW key's own window) here would mislabel it the instant the window
  // changes, before the new key's own fetch has resolved either way.
  if (query.data && !query.isPlaceholderData) {
    lastSuccess.current = query.data
    lastSuccessWindow.current = w
  }
  const data = query.data ?? lastSuccess.current
  // Falls back to the live picker selection only when nothing has EVER
  // succeeded yet (a fresh mount still pending) - there is no stale window to
  // preserve in that case.
  const dataWindow = query.data && !query.isPlaceholderData ? w : (lastSuccessWindow.current ?? w)

  return {
    jobs: data?.jobs ?? [],
    total: data?.total ?? 0,
    truncated: data?.truncated ?? false,
    sinceIso: data?.sinceIso ?? '',
    untilIso: data?.untilIso ?? '',
    window: dataWindow,
    isLoading: query.isLoading,
    isFetching: query.isFetching,
    // NEVER suppressed by the presence of data. query.error can be populated
    // even while query.data still holds the last successful fetch - a background
    // refetch failure, or a placeholderData fallback across a window/filter
    // change whose own fetch then fails - and hiding it in either case leaves
    // the caption describing a bound the failed fetch never confirmed, with no
    // sign anything went wrong. The view decides what to draw from jobs.length
    // and error together; it does not receive a pre-collapsed either/or.
    error: (query.error as Error | null) ?? null,
    refetch: () => {
      void query.refetch()
    },
  }
}
