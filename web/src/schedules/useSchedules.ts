import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listSchedules, type ScheduleSort } from './api'

// Polls one page of schedules. keepPreviousData avoids flashing empty on
// re-sort/paging and between polls. Schedules are low-churn, so the default
// interval is 10s (tests inject a small value). The relative "next run"
// countdown is ticked client-side by the page, not by this poll.
export function useSchedules(sort: ScheduleSort, cursor?: string, intervalMs = 10000) {
  return useQuery({
    queryKey: ['schedules', sort, cursor ?? ''],
    queryFn: () => listSchedules(sort, cursor),
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
