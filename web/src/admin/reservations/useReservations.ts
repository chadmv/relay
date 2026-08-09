import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listReservations, type ReservationsPage, type ReservationSort } from './api'

// Same shape as useAgentEnrollments (web/src/admin/enrollments/useAgentEnrollments.ts:14-20),
// including the deliberate absence of refetchInterval: reservations change only when
// an admin changes them, so polling them is pointless load. Freshness of the STATUS
// pill comes from useNow, a local 60s clock tick that issues no request; freshness of
// the ROW SET comes from useReservationActions invalidating the bare ['reservations']
// prefix.
//
// keepPreviousData keeps rows visible while a new sort/page loads, which is also what
// makes isPlaceholderData usable to disable the pager mid-fetch.
export function useReservations(sort: ReservationSort, cursor: string) {
  return useQuery<ReservationsPage>({
    queryKey: ['reservations', sort, cursor],
    queryFn: () => listReservations({ sort, cursor }),
    placeholderData: keepPreviousData,
  })
}
