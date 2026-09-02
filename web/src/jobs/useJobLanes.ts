import { keepPreviousData, useQueries } from '@tanstack/react-query'
import { listJobsByStatus, type Job, type JobStatus } from './api'
import { LANE_LIMIT, LANE_ORDER } from './lanes'

// One lane's whole render input. The view gets this instead of a UseQueryResult so
// it cannot mis-zip a results array against LANE_ORDER, and so its tests can state
// a lane's state directly instead of driving a query to reach it.
export interface LaneState {
  status: JobStatus
  items: Job[]
  // The status's all-time total, from the same response as the items.
  total: number
  isLoading: boolean
  isFetching: boolean
  error: Error | null
  refetch: () => void
}

// One capped list per status, polled together on the jobs cadence. `enabled` gates
// all five at once, so leaving the lanes view stops the polling rather than leaving
// five queries running behind the table.
//
// Keys are ['job-lanes', ...] and deliberately NOT under the 'jobs' prefix: a broad
// invalidateQueries(['jobs']) must not fan out into five more requests. Same
// argument as useJobStats' ['job-stats'] key; the guard here is
// useJobLanes.test.tsx's 'invalidating the jobs list does not refetch the lanes'.
export function useJobLanes(enabled: boolean, limit = LANE_LIMIT, intervalMs = 3000): LaneState[] {
  const results = useQueries({
    queries: LANE_ORDER.map((status) => ({
      queryKey: ['job-lanes', status, limit],
      queryFn: () => listJobsByStatus(status, limit),
      enabled,
      refetchInterval: intervalMs,
      placeholderData: keepPreviousData,
    })),
  })

  return LANE_ORDER.map((status, i) => {
    const r = results[i]
    return {
      status,
      items: r.data?.items ?? [],
      total: r.data?.total ?? 0,
      isLoading: r.isLoading && !r.data,
      isFetching: r.isFetching,
      // A failed poll that still has rows keeps showing them: the error surfaces
      // only when there is nothing else to render, matching the table view's own
      // `error && !data` rule.
      error: r.data ? null : ((r.error as Error | null) ?? null),
      refetch: () => {
        void r.refetch()
      },
    }
  })
}
