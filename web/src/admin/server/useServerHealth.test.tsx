import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../../test/setup-helpers'
import { HEALTH_POLL_MS, useServerHealth } from './useServerHealth'
import type { HealthResponse } from './api'

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

test('caches under ["server-health"]', async () => {
  server.use(http.get('/v1/health', () => HttpResponse.json({ status: 'ok' })))
  const client = newClient()
  const { result } = renderHook(() => useServerHealth(), { wrapper: makeWrapper(client) })
  await waitFor(() => expect(result.current.status).toBe('success'))
  expect(client.getQueryData<HealthResponse>(['server-health'])?.status).toBe('ok')
})

test('polls at the 30s reachability cadence, not faster', async () => {
  server.use(http.get('/v1/health', () => HttpResponse.json({ status: 'ok' })))
  const client = newClient()
  const { result } = renderHook(() => useServerHealth(), { wrapper: makeWrapper(client) })
  await waitFor(() => expect(result.current.status).toBe('success'))

  // Read the actual wiring off the query cache, not a copy of the constant: a
  // hook that passes a different literal (e.g. 3000) while HEALTH_POLL_MS stays
  // 30_000 must fail this test.
  const query = client.getQueryCache().find({ queryKey: ['server-health'] })
  expect((query?.options as { refetchInterval?: number })?.refetchInterval).toBe(HEALTH_POLL_MS)
  expect(HEALTH_POLL_MS).toBe(30_000)
})

test('an error leaves data undefined so the pill can distinguish unreachable from checking', async () => {
  server.use(http.get('/v1/health', () => HttpResponse.json({ error: 'boom' }, { status: 500 })))
  const { result } = renderHook(() => useServerHealth(), { wrapper: makeWrapper(newClient()) })
  await waitFor(() => expect(result.current.status).toBe('error'))
  expect(result.current.data).toBeUndefined()
})
