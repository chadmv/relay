import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listWorkerWorkspaces } from './api'

// Polls a worker's source workspaces. Admin-only data; this hook is only mounted
// for admins (WorkerDetailPage gates rendering of the panel), so no enabled flag
// is needed - a non-admin page never mounts the panel and never fires this
// request. Slow cadence since workspaces change rarely. Tests inject a small value.
export function useWorkerWorkspaces(id: string, intervalMs = 15000) {
  return useQuery({
    queryKey: ['worker', id, 'workspaces'],
    queryFn: () => listWorkerWorkspaces(id),
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
