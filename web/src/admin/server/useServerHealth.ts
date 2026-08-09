import { useQuery } from '@tanstack/react-query'
import { getHealth } from './api'

// A reachability probe, not a dashboard: 30s is enough to notice the listener
// going away. There is exactly one consumer (the Server tab, and only one admin
// tab renders at a time), so 30s is chosen as a liveness cadence, not as a
// load-sharing compromise across multiple consumers.
export const HEALTH_POLL_MS = 30_000

export function useServerHealth() {
  return useQuery({
    queryKey: ['server-health'],
    queryFn: getHealth,
    refetchInterval: HEALTH_POLL_MS,
  })
}
