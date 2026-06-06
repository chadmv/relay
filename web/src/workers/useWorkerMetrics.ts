import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { getWorkerMetrics } from './api'

// Polls a worker's telemetry. Default 10000 matches the 10s server sample
// cadence; polling faster only re-fetches identical data. Tests inject a small value.
export function useWorkerMetrics(id: string, intervalMs = 10000) {
  return useQuery({
    queryKey: ['worker', id, 'metrics'],
    queryFn: () => getWorkerMetrics(id),
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
