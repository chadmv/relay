import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { useScheduleStats } from './useScheduleStats'

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

// Hand-written, no production type annotation. All five keys, because the server
// response has no omitempty.
const STATS_BODY = { enabled: 12, paused: 5, total: 17, failed_runs_24h: 3, failing: 2 }

// A SECOND CALL, not just a first one. A hook with no refetchInterval satisfies
// "it fetched"; only the second arrival distinguishes a polling query from a
// one-shot, which is what the strip sitting above a 10s-polling list needs.
test('fetches stats and refetches on the interval', async () => {
  let count = 0
  server.use(
    http.get('/v1/scheduled-jobs/stats', () => {
      count++
      return HttpResponse.json(STATS_BODY)
    }),
  )

  renderHook(() => useScheduleStats(20), { wrapper })

  await waitFor(() => expect(count).toBeGreaterThanOrEqual(1))
  await waitFor(() => expect(count).toBeGreaterThanOrEqual(2))
})

test('requests the stats path, not the list path', async () => {
  let path: string | undefined
  server.use(
    http.get('/v1/scheduled-jobs/stats', ({ request }) => {
      path = new URL(request.url).pathname
      return HttpResponse.json(STATS_BODY)
    }),
  )
  const { result } = renderHook(() => useScheduleStats(20), { wrapper })
  await waitFor(() => expect(result.current.data).toBeDefined())
  expect(path).toBe('/v1/scheduled-jobs/stats')
})
