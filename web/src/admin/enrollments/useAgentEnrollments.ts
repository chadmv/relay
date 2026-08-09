import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listAgentEnrollments, type AgentEnrollmentsPage, type EnrollmentSort } from './api'

// The list query for the admin Agent-enrollments tab. Same shape as
// useAdminUsers (web/src/admin/users/useAdminUsers.ts:9-20), including the
// deliberate absence of refetchInterval: this is not live data, so polling it is
// pointless load. Freshness of the EXPIRING/EXPIRED pill comes from useNow, a
// local 60s clock tick that issues no request; freshness of the ROW SET comes
// from useAgentEnrollmentActions invalidating the bare ['agent-enrollments']
// prefix.
//
// keepPreviousData keeps rows visible while a new sort/page loads, which is also
// what makes isPlaceholderData usable to disable the pager mid-fetch.
export function useAgentEnrollments(sort: EnrollmentSort, cursor: string) {
  return useQuery<AgentEnrollmentsPage>({
    queryKey: ['agent-enrollments', sort, cursor],
    queryFn: () => listAgentEnrollments({ sort, cursor }),
    placeholderData: keepPreviousData,
  })
}
