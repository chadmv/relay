import { useQuery } from '@tanstack/react-query'
import { listReservations, type ReservationsPage } from '../admin/reservations/api'

// The worker detail page's reservations panel.
//
// The key's middle element is the literal 'worker', which is not a member of
// ReservationSort, so this key cannot collide with the admin tab's
// ['reservations', sort, cursor]. The FIRST element is deliberately the same:
// useReservationActions invalidates the bare ['reservations'] prefix, so an
// admin's create or delete refreshes this panel too.
//
// No refetchInterval, matching useReservations: reservations change only when an
// admin changes them. The STATUS pill's freshness comes from the caller's clock
// tick, which issues no request.
//
// No placeholderData: the key changes only when the route id does, and a previous
// worker's page shown under this worker's name is a claim the panel cannot back.
//
// No `enabled` gate, unlike useWorkerTasks: this hook is mounted by a component
// that renders BELOW the page's loading and not-found early returns and inside
// its admin branch, so a 404 worker and a non-admin viewer both mount nothing.
export function useWorkerReservations(workerId: string) {
  return useQuery<ReservationsPage>({
    queryKey: ['reservations', 'worker', workerId],
    queryFn: () => listReservations({ sort: '-created_at', cursor: '', workerId }),
  })
}
