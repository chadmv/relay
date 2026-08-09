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
  let calls = 0
  server.use(
    http.get('/v1/health', () => {
      calls++
      return HttpResponse.json({ status: 'ok' })
    }),
  )
  const { result } = renderHook(() => useServerHealth(), { wrapper: makeWrapper(newClient()) })
  await waitFor(() => expect(result.current.status).toBe('success'))
  expect(calls).toBe(1)
  expect(HEALTH_POLL_MS).toBe(30_000)

  // A real-time wait can only rule out an interval SHORTER than the wait, so this
  // 250ms window catches a copy-pasted 3000/10_000 only via the constant assertion
  // above - which is the load-bearing check. The wait guards against an accidental
  // sub-250ms interval, which no copy-paste source in this repo has.
  await new Promise((r) => setTimeout(r, 250))
  expect(calls).toBe(1)

  // Positive control on the SAME counter, so the equality above is about polling
  // and not a dead instrument.
  await result.current.refetch()
  await waitFor(() => expect(calls).toBe(2))
})

test('an error leaves data undefined so the pill can distinguish unreachable from checking', async () => {
  server.use(http.get('/v1/health', () => HttpResponse.json({ error: 'boom' }, { status: 500 })))
  const { result } = renderHook(() => useServerHealth(), { wrapper: makeWrapper(newClient()) })
  await waitFor(() => expect(result.current.status).toBe('error'))
  expect(result.current.data).toBeUndefined()
})
