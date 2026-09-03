import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { useNow } from '../lib/useNow'
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
  isLoading: boolean
  isFetching: boolean
  error: Error | null
  refetch: () => void
}

export interface TimelineWalk {
  jobs: Job[]
  total: number
  truncated: boolean
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

  return { jobs, total, truncated: pages >= TIMELINE_MAX_PAGES && cursor !== '' }
}

/**
 * One window of jobs, drawn from up to TIMELINE_MAX_PAGES pages.
 *
 * There is no refetchInterval. The anchor's own advance IS the refresh: it moves
 * once per ANCHOR_STEP_MS, which mints a new key. A refetchInterval on a stable
 * key would re-walk three pages on every tick instead.
 *
 * gcTime is explicit and short. Every anchor step mints a new key, so at the
 * client default of five minutes the cache would accumulate about twenty
 * abandoned entries, each holding up to a full walk's rows. This is a consequence
 * of a per-tick key that nothing else in the app has, so it is a requirement
 * rather than a tuning note.
 */
export function useJobTimeline(
  enabled: boolean,
  w: TimelineWindow,
  q: string,
  mine: boolean,
): TimelineState {
  const nowMs = useNow(ANCHOR_STEP_MS).getTime()
  const { sinceIso, untilIso } = windowBounds(w, nowMs)

  const query = useQuery({
    queryKey: ['job-timeline', sinceIso, untilIso, q, mine],
    queryFn: ({ signal }) => walkJobWindow({ sinceIso, untilIso, q, mine }, signal),
    enabled,
    gcTime: 60_000,
    placeholderData: keepPreviousData,
  })

  return {
    jobs: query.data?.jobs ?? [],
    total: query.data?.total ?? 0,
    truncated: query.data?.truncated ?? false,
    sinceIso,
    untilIso,
    isLoading: query.isLoading,
    isFetching: query.isFetching,
    // A failed refresh that still has rows keeps showing them; the error surfaces
    // only when there is nothing else to render, matching the table view's rule.
    error: query.data ? null : ((query.error as Error | null) ?? null),
    refetch: () => {
      void query.refetch()
    },
  }
}
