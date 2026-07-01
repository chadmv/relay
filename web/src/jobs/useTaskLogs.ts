import { useQuery } from '@tanstack/react-query'
import { getTaskLogs } from './api'

// Static historical log for a single task. NO refetchInterval: the log is
// fetch-once (live tailing/SSE is a separate deferred slice). `enabled` is
// controlled by the caller so we never fetch logs for a task the user has not
// opened, and never fetch while the Spec tab (not the Log tab) is showing.
// The key is deliberately NOT under the ['job', ...] prefix, so a job poll
// invalidation never disturbs the log query.
export function useTaskLogs(taskId: string, enabled: boolean) {
  return useQuery({
    queryKey: ['task-logs', taskId],
    queryFn: () => getTaskLogs(taskId),
    enabled,
    staleTime: Infinity,
  })
}
