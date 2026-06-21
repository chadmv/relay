// TDD test asserting that invalidateQueries(['jobs']) does NOT trigger a
// refetch of the stats query.
//
//   RED  (before fix): useJobStats key is ['jobs', 'stats'] - it shares the
//        'jobs' prefix so a broad invalidateQueries(['jobs']) also refetches
//        the stats query. The assertion expect(statsCalls).toBe(1) fails.
//
//   GREEN (after fix): useJobStats key becomes ['job-stats'] which does NOT
//        match the 'jobs' prefix, so the stats query is left untouched.
//
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor, act } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { useJobs } from './useJobs'
import { useJobStats } from './useJobStats'

function makeWrapper(client: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>
  }
}

test('invalidating the jobs list does not refetch the stats query', async () => {
  // Track how many times each endpoint is hit.
  let jobsCalls = 0
  let statsCalls = 0

  server.use(
    http.get('/v1/jobs', () => {
      jobsCalls++
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
    http.get('/v1/jobs/stats', () => {
      statsCalls++
      return HttpResponse.json({ running: 1, queued: 0, done_24h: 0, failed_24h: 0 })
    }),
  )

  // Large intervals so periodic refetches never fire during the test.
  const intervalMs = 100_000

  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const wrapper = makeWrapper(client)

  // Render both hooks sharing the same QueryClient.
  const { result: statsResult } = renderHook(() => useJobStats(intervalMs), {
    wrapper,
  })
  const { result: jobsResult } = renderHook(
    () => useJobs('-created_at', '', '', intervalMs),
    { wrapper },
  )

  // Wait for both initial fetches to complete.
  await waitFor(() => {
    expect(statsResult.current.status).toBe('success')
    expect(jobsResult.current.status).toBe('success')
  })

  // Confirm one fetch each so far.
  expect(statsCalls).toBe(1)
  expect(jobsCalls).toBe(1)

  // Broad invalidation of ['jobs'] - this must NOT touch the stats query.
  await act(async () => {
    await client.invalidateQueries({ queryKey: ['jobs'] })
  })

  // Wait for the jobs list to be refetched (at least 2 total calls).
  await waitFor(() => expect(jobsCalls).toBeGreaterThanOrEqual(2))

  // Stats must still be at exactly 1 call - not refetched.
  // Before the fix this fails: stats key ['jobs','stats'] matches ['jobs'] prefix,
  // so it is also invalidated and refetched.
  // After the fix this passes: stats key ['job-stats'] does not match.
  expect(statsCalls).toBe(1)
})
