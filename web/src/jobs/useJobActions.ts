import { useMutation, useQueryClient } from '@tanstack/react-query'
import { cancelJob, retryJob, type RetryMode } from './api'

// Cancel and retry mutations for the job-detail actions bar. Follows the
// invalidate-on-success strategy of useWorkerActions. Key invariants:
//  - ONE mutation per action; the mode is its variable (cancel.mutate(false|true),
//    retry.mutate('failed'|'all')). The only observable difference is the query
//    param the request carries.
//  - onSuccess invalidates ['job', id], ['jobs'], ['job-stats'] and
//    ['job-lanes']. The last two are decoupled from ['jobs'] (see
//    queryKeyDecoupling.test.tsx), so the bare ['jobs'] invalidation alone would
//    leave the KPI strip stale and the lanes showing a cancelled job as running.
//  - ['job', id] IS invalidated (a cancelled job is still viewable); the caller
//    stays on the detail page. This is the opposite of worker revoke.
//  - No optimistic update; useJob polls ['job', id] every 3s and the invalidate
//    triggers an immediate refetch.
//  - RETRY INVALIDATES, IT DOES NOT SEED. The 200 body is built with
//    toJobResponse(job, "", nil, nil) (internal/api/jobs.go, handleRetryJob), so
//    total_tasks/done_tasks are 0 and `tasks` is absent entirely. Writing it into
//    ['job', id] would blank the task table, the DAG and the progress strip. See
//    RetryResult in api.ts.
//  - There is no further key. Task statuses live INSIDE the ['job', id] payload
//    (useJob), and log lines never enter TanStack at all (useTaskLogStream keeps
//    them in component state), so nothing else caches what a retry changes.
export function useJobActions(id: string) {
  const qc = useQueryClient()

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['job', id] })
    qc.invalidateQueries({ queryKey: ['jobs'] })
    qc.invalidateQueries({ queryKey: ['job-stats'] })
    qc.invalidateQueries({ queryKey: ['job-lanes'] })
  }

  const cancel = useMutation({
    mutationFn: (force: boolean) => cancelJob(id, force),
    onSuccess: invalidate,
  })

  const retry = useMutation({
    mutationFn: (mode: RetryMode) => retryJob(id, mode),
    onSuccess: invalidate,
  })

  return { cancel, retry }
}
