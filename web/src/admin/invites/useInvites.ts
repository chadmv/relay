import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listInvites, type InvitesPage, type InviteSort } from './api'

// The list query for the admin Invites tab. Same shape as useAgentEnrollments
// (web/src/admin/enrollments/useAgentEnrollments.ts:14-20), including the
// deliberate absence of refetchInterval: this is not live data, so polling it is
// pointless load. Freshness of the EXPIRING/EXPIRED pill comes from useNow, a
// local 60s clock tick that issues no request; freshness of the ROW SET comes from
// useInviteActions invalidating the bare ['invites'] prefix.
//
// keepPreviousData keeps rows visible while a new sort/page loads, which is also
// what makes isPlaceholderData usable to disable the pager mid-fetch.
export function useInvites(sort: InviteSort, cursor: string) {
  return useQuery<InvitesPage>({
    queryKey: ['invites', sort, cursor],
    queryFn: () => listInvites({ sort, cursor }),
    placeholderData: keepPreviousData,
  })
}
