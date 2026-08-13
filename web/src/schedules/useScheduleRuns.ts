import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listJobsBySchedule } from '../jobs/api'

// A fixed latest-N window on a detail page, NOT a list page: no cursor stack, no
// pager, no sort control, and none of the computePageRange/offsets machinery. The
// panel's footer states the window honestly as "latest N of <total>".
export const SCHEDULE_RUNS_LIMIT = 20

// 10s, matching useSchedule and useSchedules (useSchedules.ts:8). The key nests under
// the bare ['schedules'] prefix so runNow/setEnabled/update/remove all refresh this
// panel through the invalidation they already perform - a job created by Run now
// appears without a reload. 'runs' is not a ScheduleSort (api.ts:28-36).
export function useScheduleRuns(id: string, intervalMs = 10000) {
  return useQuery({
    queryKey: ['schedules', 'runs', id],
    queryFn: () => listJobsBySchedule(id, SCHEDULE_RUNS_LIMIT),
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
