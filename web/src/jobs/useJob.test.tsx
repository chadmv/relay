import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { useJob } from './useJob'

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

test('fetches the job and refetches on the interval', async () => {
  let count = 0
  server.use(
    http.get('/v1/jobs/j1', () => {
      count++
      return HttpResponse.json({
        id: 'j1', name: 'render', priority: 'high', status: 'running',
        submitted_by: 'u1', labels: null, tasks: [],
        created_at: '2026-07-01T00:00:00Z', updated_at: '2026-07-01T00:00:00Z',
      })
    }),
  )
  const { result } = renderHook(() => useJob('j1', 20), { wrapper })
  await waitFor(() => expect(count).toBeGreaterThanOrEqual(2))
  await waitFor(() => expect(result.current.data?.name).toBe('render'))
})
