import { useMutation, useQueryClient } from '@tanstack/react-query'
import { createJob } from './api'

// Create-job mutation. Router-free by design: the page owns navigation in its
// own onSuccess. On success it invalidates TWO keys:
//   - ['jobs']       (bare prefix) so every list view ['jobs', sort, status,
//     cursor] refetches and the new job appears.
//   - ['job-stats']  MUST be explicit; it is decoupled from ['jobs'] (see
//     queryKeyDecoupling.test.tsx), so ['jobs'] alone leaves the KPI strip stale.
// There is NO ['job', id] to invalidate: the job is brand new and not yet cached.
// No optimistic update.
export function useCreateJob() {
  const qc = useQueryClient()

  return useMutation({
    mutationFn: (spec: unknown) => createJob(spec),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['jobs'] })
      qc.invalidateQueries({ queryKey: ['job-stats'] })
    },
  })
}
