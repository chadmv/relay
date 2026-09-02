import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listJobs, type JobSort } from './api'

// Polls one page of jobs. keepPreviousData keeps rows visible while a new
// sort/filter/page loads and between polls, so the table never flashes empty.
// intervalMs defaults to 3000; tests inject a small value. enabled gates the poll
// so a page showing a different view can stop fetching a 50-row page nobody is
// looking at; useJobs.enabled.test.tsx is the guard.
export function useJobs(sort: JobSort, status: string, cursor: string, intervalMs = 3000, enabled = true) {
  return useQuery({
    queryKey: ['jobs', sort, status, cursor],
    queryFn: () => listJobs(sort, status, cursor),
    enabled,
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
