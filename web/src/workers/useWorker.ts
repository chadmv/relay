import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { getWorker } from './api'

// Polls a single worker's identity/status. Default 3000 matches the list page,
// keeping status/last_seen live. Tests inject a small value.
export function useWorker(id: string, intervalMs = 3000) {
  return useQuery({
    queryKey: ['worker', id],
    queryFn: () => getWorker(id),
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
