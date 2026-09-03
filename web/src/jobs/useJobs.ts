import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listJobs, type JobSort } from './api'

// Polls one page of jobs. keepPreviousData keeps rows visible while a new
// sort/filter/page loads and between polls, so the table never flashes empty.
// intervalMs defaults to 3000; tests inject a small value. enabled gates the poll
// so a page showing a different view can stop fetching a 50-row page nobody is
// looking at; useJobs.enabled.test.tsx is the guard.
//
// q and mine are in the KEY as well as the call. A cursor carries no record of
// the filters it was issued under and the server does not reject a mismatched
// one, so the caller must also drop the cursor when either changes; the key
// alone would only re-fetch, not re-page.
export function useJobs(
  sort: JobSort,
  status: string,
  cursor: string,
  intervalMs = 3000,
  enabled = true,
  q = '',
  mine = false,
) {
  return useQuery({
    queryKey: ['jobs', sort, status, cursor, q, mine],
    queryFn: () => listJobs(sort, status, cursor, q, mine),
    enabled,
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
