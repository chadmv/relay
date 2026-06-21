import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listRevokedWorkers } from './api'

// Polls one page of revoked workers. enabled gates the query so it only
// runs while the Decommissioned tab is active. cursor selects the page.
// intervalMs defaults to 3000; tests inject a small value.
export function useRevokedWorkers(enabled: boolean, cursor = '', intervalMs = 3000) {
  return useQuery({
    queryKey: ['workers', 'revoked', cursor],
    queryFn: () => listRevokedWorkers(cursor),
    enabled,
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
