import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { useWorkerWorkspaces } from './useWorkerWorkspaces'

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

test('fetches workspaces and refetches on the interval', async () => {
  let count = 0
  server.use(
    http.get('/v1/workers/w1/workspaces', () => {
      count++
      return HttpResponse.json([
        { source_type: 'perforce', source_key: '//depot/x', short_id: 'ws-1', baseline_hash: '@1', last_used_at: '2026-06-05T00:00:00Z' },
      ])
    }),
  )
  const { result } = renderHook(() => useWorkerWorkspaces('w1', 20), { wrapper })
  await waitFor(() => expect(count).toBeGreaterThanOrEqual(1))
  await waitFor(() => expect(count).toBeGreaterThanOrEqual(2))
  await waitFor(() => expect(result.current.data?.[0].short_id).toBe('ws-1'))
})
