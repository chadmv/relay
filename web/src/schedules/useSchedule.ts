import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { getSchedule } from './api'

// Polls one schedule. 10s matches useSchedules (useSchedules.ts:8) - a schedule row
// is equally low-churn. useJobs' 3s (useJobs.ts:7) is for a live fleet view and is
// deliberately NOT copied here. The relative countdown on the page is ticked by
// useNow(1000), a local clock that issues no request, not by this poll.
//
// The key nests under the existing bare ['schedules'] prefix so the invalidations
// useScheduleActions already performs (useScheduleActions.ts:11, :16) reach it with
// NO change to those two shipped mutations. 'detail' is not a ScheduleSort
// (api.ts:28-36), so it cannot collide with ['schedules', sort, cursor].
export function useSchedule(id: string, intervalMs = 10000) {
  return useQuery({
    queryKey: ['schedules', 'detail', id],
    queryFn: () => getSchedule(id),
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
