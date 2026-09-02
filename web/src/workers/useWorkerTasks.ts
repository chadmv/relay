import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listWorkerTasks } from './api'

interface WorkerTasksOptions {
  intervalMs?: number
  enabled?: boolean
}

// Polls a worker's currently assigned tasks. The Slots KPI is composed from this
// query AND the worker query, so a cadence that did not match the worker poll
// would put the two halves of one displayed fraction an interval apart. Tests
// inject a small value.
//
// A caller that mounts this hook above its own not-found early return must
// pass enabled: false once the worker read has failed, or the poll outlives
// the worker it is asking about.
//
// keepPreviousData means a changing route id can render worker A's rows under
// worker B's id for one tick. The query key includes id, so the stale render is
// transient, and callers that display a NUMBER from it must gate on
// isPlaceholderData rather than on data alone.
export function useWorkerTasks(id: string, { intervalMs = 3000, enabled = true }: WorkerTasksOptions = {}) {
  return useQuery({
    queryKey: ['worker', id, 'tasks'],
    queryFn: () => listWorkerTasks(id),
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
    enabled,
  })
}
