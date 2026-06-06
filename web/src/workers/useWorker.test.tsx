import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { useWorker } from './useWorker'

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

test('fetches the worker and refetches on the interval', async () => {
  let count = 0
  server.use(
    http.get('/v1/workers/w1', () => {
      count++
      return HttpResponse.json({ id: 'w1', name: 'render-01', status: 'online' })
    }),
  )
  const { result } = renderHook(() => useWorker('w1', 20), { wrapper })
  await waitFor(() => expect(count).toBeGreaterThanOrEqual(1))
  await waitFor(() => expect(count).toBeGreaterThanOrEqual(2))
  await waitFor(() => expect(result.current.data?.name).toBe('render-01'))
})
