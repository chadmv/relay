import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { getWorkerStats } from './api'

// Polls fleet-wide worker counts for the summary strip. Same cadence as
// useWorkers. intervalMs defaults to 3000; tests inject a small value.
export function useWorkerStats(intervalMs = 3000) {
  return useQuery({
    queryKey: ['workers', 'stats'],
    queryFn: getWorkerStats,
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
