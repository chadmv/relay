import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listWorkerTasks } from './api'

// Polls a worker's currently assigned tasks. The default 3000 matches useWorker:
// the Slots KPI is composed from BOTH queries, so a mismatched cadence would put
// the two halves of one displayed fraction an interval apart. Tests inject a
// small value.
//
// keepPreviousData matches its two siblings. With a changing route id it can
// render worker A's rows under worker B's id for one tick; the query key
// includes id, so that render is transient and self-corrects, and this panel
// issues no writes, so the confused-deputy form of that hazard does not apply.
export function useWorkerTasks(id: string, intervalMs = 3000) {
  return useQuery({
    queryKey: ['worker', id, 'tasks'],
    queryFn: () => listWorkerTasks(id),
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
