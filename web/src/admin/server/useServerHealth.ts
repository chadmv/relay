import { useQuery } from '@tanstack/react-query'
import { getHealth } from './api'

// A reachability probe, not a dashboard: 30s is enough to notice the listener
// going away, and this endpoint is polled by every open admin tab.
export const HEALTH_POLL_MS = 30_000

export function useServerHealth() {
  return useQuery({
    queryKey: ['server-health'],
    queryFn: getHealth,
    refetchInterval: HEALTH_POLL_MS,
  })
}
