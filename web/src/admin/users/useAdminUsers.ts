import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listUsers, type AdminUsersPage, type UserSort } from './api'

// The list query for the admin Users tab. Deliberately NO refetchInterval: unlike
// workers/jobs this is not live data, so polling it every 3s is pointless load.
// Refresh comes from useAdminUserActions invalidating the bare ['users'] prefix.
// keepPreviousData keeps rows visible while a new sort / page / filter loads, which
// is also what makes isPlaceholderData usable to disable the pager mid-fetch.
export function useAdminUsers(
  sort: UserSort,
  includeArchived: boolean,
  cursor: string,
  email: string,
) {
  return useQuery<AdminUsersPage>({
    queryKey: ['users', sort, includeArchived, cursor, email],
    queryFn: () => listUsers({ sort, includeArchived, cursor, email }),
    placeholderData: keepPreviousData,
  })
}
