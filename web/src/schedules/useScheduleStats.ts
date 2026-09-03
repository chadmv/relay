import { useQuery } from '@tanstack/react-query'
import { getScheduleStats } from './api'

// The Schedules summary strip's fleet-wide census.
//
// 10s, the same cadence as useSchedules, not the 3s useWorkerStats uses: a strip
// refreshes with the list it sits above, and a header that refreshes faster than
// its rows has moved on from what is underneath it.
//
// NO placeholderData: keepPreviousData. The key is constant - the endpoint takes
// no filters and no cursor - so nothing ever mints a new key and the option would
// be inert. Stated so it is not added back for symmetry with useWorkerStats.
//
// Not gated by `enabled`: there is one view on this page and the strip is always
// mounted. The key sits under the bare ['schedules'] prefix that
// useScheduleActions invalidates, so an Enable/Disable click refreshes the strip
// with the list.
export function useScheduleStats(intervalMs = 10000) {
  return useQuery({
    queryKey: ['schedules', 'stats'],
    queryFn: getScheduleStats,
    refetchInterval: intervalMs,
  })
}
