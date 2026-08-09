import { useQuery } from '@tanstack/react-query'
import { listWorkers, type WorkersPage } from '../../workers/api'

// The server's maxLimit (internal/api/pagination.go:207). Asking for more is a 400,
// not a clamp, so this is the hard ceiling of a single-request picker.
export const WORKER_PICKER_LIMIT = 200

// Worker options for the reservation create form. Deliberately NOT useWorkers:
//  - useWorkers polls every 3s for the live workers page; a form does not need that,
//    and a list that reorders under the admin's cursor mid-selection is hostile.
//  - staleTime keeps the list stable while the panel is open and across reopens.
//
// The key sits under the BARE ['workers'] prefix, so the shipped worker mutations
// (useWorkerActions.ts:26, :50, :73, :82) invalidate it for free, and
// 'reservation-picker' is not a WorkerSort so it cannot collide with
// useWorkers' ['workers', sort].
//
// CEILING, stated rather than hidden: this is ONE page of at most 200 workers by
// name, with no cursor. WorkerPicker renders a visible note whenever
// total > items.length. A genuinely paginated or server-filtered picker is a bigger
// unit and belongs in its own backlog item if a fleet ever exceeds 200 workers - it
// must never silently offer a truncated list as if it were complete.
export function useWorkerOptions() {
  return useQuery<WorkersPage>({
    queryKey: ['workers', 'reservation-picker'],
    queryFn: () => listWorkers('name', WORKER_PICKER_LIMIT),
    staleTime: 30_000,
  })
}
