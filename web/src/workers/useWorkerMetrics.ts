import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { getWorkerMetrics } from './api'

interface WorkerMetricsOptions {
  intervalMs?: number
  enabled?: boolean
}

// Polls a worker's telemetry. Default 10000 matches the 10s server sample
// cadence; polling faster only re-fetches identical data. Tests inject a small value.
//
// A caller that mounts this hook above its own not-found early return must
// pass enabled: false once the worker read has failed, or the poll outlives
// the worker it is asking about.
export function useWorkerMetrics(id: string, { intervalMs = 10000, enabled = true }: WorkerMetricsOptions = {}) {
  return useQuery({
    queryKey: ['worker', id, 'metrics'],
    queryFn: () => getWorkerMetrics(id),
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
    enabled,
  })
}
