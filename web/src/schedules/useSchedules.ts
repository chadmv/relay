import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listSchedules, type ScheduleListFilters, type ScheduleSort } from './api'

const NO_FILTERS: ScheduleListFilters = { enabledKey: 'all', q: '' }

// Polls one page of schedules. keepPreviousData avoids flashing empty on
// re-sort/paging/filtering and between polls. Schedules are low-churn, so the
// default interval is 10s (tests inject a small value). The relative "next run"
// countdown is ticked client-side by the page, not by this poll.
//
// `filters` is the FOURTH positional parameter, after the interval, so every
// existing call site compiles unchanged. The key carries the chip KEY rather than
// the wire value, so the three states are three distinct strings and no undefined
// or boolean has to be hashed.
export function useSchedules(
  sort: ScheduleSort,
  cursor?: string,
  intervalMs = 10000,
  filters: ScheduleListFilters = NO_FILTERS,
) {
  return useQuery({
    queryKey: ['schedules', sort, cursor ?? '', filters.enabledKey, filters.q],
    queryFn: () => listSchedules(sort, cursor, filters),
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
