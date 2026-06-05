import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { useWorkerStats } from './useWorkerStats'

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

test('fetches stats and refetches on the interval', async () => {
  let count = 0
  server.use(
    http.get('/v1/workers/stats', () => {
      count++
      return HttpResponse.json({ online: 1, stale: 0, offline: 0, disabled: 0, total: 1 })
    }),
  )

  const { result } = renderHook(() => useWorkerStats(20), { wrapper })

  await waitFor(() => expect(count).toBeGreaterThanOrEqual(1))
  await waitFor(() => expect(count).toBeGreaterThanOrEqual(2))
  await waitFor(() => expect(result.current.data?.total).toBe(1))
})
