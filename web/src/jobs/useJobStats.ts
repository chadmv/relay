import { useQuery } from '@tanstack/react-query'
import { getJobStats } from './api'

export function useJobStats(intervalMs = 3000) {
  return useQuery({
    queryKey: ['job-stats'],
    queryFn: getJobStats,
    refetchInterval: intervalMs,
  })
}
