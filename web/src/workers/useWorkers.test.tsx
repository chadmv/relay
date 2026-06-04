import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { useWorkers } from './useWorkers'

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

test('fetches workers and refetches on the interval', async () => {
  let count = 0
  server.use(
    http.get('/v1/workers', () => {
      count++
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )

  renderHook(() => useWorkers('-created_at', 20), { wrapper })

  await waitFor(() => expect(count).toBeGreaterThanOrEqual(1))
  await waitFor(() => expect(count).toBeGreaterThanOrEqual(2))
})
