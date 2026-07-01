import { useMutation, useQueryClient } from '@tanstack/react-query'
import { cancelJob } from './api'

// Cancel mutation for the job-detail actions bar. Follows the invalidate-on-
// success strategy of useWorkerActions. Key invariants:
//  - ONE mutation; force is its variable (cancel.mutate(false|true)). The only
//    observable difference is the ?force=true query param.
//  - onSuccess invalidates THREE keys: ['job', id], ['jobs'], and ['job-stats'].
//    ['job-stats'] is decoupled from ['jobs'] (see queryKeyDecoupling.test.tsx),
//    so the bare ['jobs'] invalidation alone would leave the KPI strip stale.
//  - ['job', id] IS invalidated (a cancelled job is still viewable); the caller
//    stays on the detail page. This is the opposite of worker revoke.
//  - No optimistic update; useJob polls ['job', id] every 3s and the invalidate
//    triggers an immediate refetch.
export function useJobActions(id: string) {
  const qc = useQueryClient()

  const cancel = useMutation({
    mutationFn: (force: boolean) => cancelJob(id, force),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['job', id] })
      qc.invalidateQueries({ queryKey: ['jobs'] })
      qc.invalidateQueries({ queryKey: ['job-stats'] })
    },
  })

  return { cancel }
}
