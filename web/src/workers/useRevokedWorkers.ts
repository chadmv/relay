import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listRevokedWorkers } from './api'

// Polls the first page of revoked workers. enabled gates the query so it only
// runs while the Decommissioned tab is active. intervalMs defaults to 3000.
export function useRevokedWorkers(enabled: boolean, intervalMs = 3000) {
  return useQuery({
    queryKey: ['workers', 'revoked'],
    queryFn: listRevokedWorkers,
    enabled,
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
