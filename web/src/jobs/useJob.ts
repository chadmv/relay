import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { getJob } from './api'

// Polls a single job's detail (identity, status, and its task list). Polling
// keeps task status/progress live without SSE. Default 3000 matches the list and
// worker-detail pages. Tests inject a small value.
export function useJob(id: string, intervalMs = 3000) {
  return useQuery({
    queryKey: ['job', id],
    queryFn: () => getJob(id),
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
