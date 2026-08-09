import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../../test/setup-helpers'
import { useServerConfig } from './useServerConfig'
import type { ConfigResponse } from '../../lib/types'

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

test('gcTime is Infinity so a remount past the default 5-minute window still hits cache', async () => {
  server.use(http.get('/v1/config', () => HttpResponse.json({ allow_self_register: false })))
  const client = newClient()
  const { result } = renderHook(() => useServerConfig(), { wrapper: makeWrapper(client) })
  await waitFor(() => expect(result.current.status).toBe('success'))
  const query = client.getQueryCache().find({ queryKey: ['server-config'] })
  expect(query?.options.gcTime).toBe(Infinity)
})

test('caches under ["server-config"]', async () => {
  server.use(http.get('/v1/config', () => HttpResponse.json({ allow_self_register: false })))
  const client = newClient()
  const { result } = renderHook(() => useServerConfig(), { wrapper: makeWrapper(client) })
  await waitFor(() => expect(result.current.status).toBe('success'))
  expect(client.getQueryData<ConfigResponse>(['server-config'])?.allow_self_register).toBe(false)
})

test('does not poll and does not refetch on a remount within the same client', async () => {
  let calls = 0
  server.use(
    http.get('/v1/config', () => {
      calls++
      return HttpResponse.json({ allow_self_register: true })
    }),
  )
  const client = newClient()
  const wrapper = makeWrapper(client)
  const first = renderHook(() => useServerConfig(), { wrapper })
  await waitFor(() => expect(first.result.current.status).toBe('success'))
  expect(calls).toBe(1)

  // The value is read from process env at startup, so it cannot change without a
  // server restart - which also restarts the SPA's own server. Unmount and remount
  // is the real scenario (tab switch), and staleTime: Infinity must make it free.
  first.unmount()
  const second = renderHook(() => useServerConfig(), { wrapper })
  await waitFor(() => expect(second.result.current.status).toBe('success'))

  // Wait past a duration that would actually catch a refetch. useJobStats polls at
  // 3000ms and useWorkerStats at 3000ms; any interval copied from this codebase, or
  // any staleTime default (0) driving a mount refetch, has fired well before 3.2s.
  await new Promise((r) => setTimeout(r, 3_200))
  expect(calls).toBe(1)

  // Positive control on the SAME counter.
  await second.result.current.refetch()
  await waitFor(() => expect(calls).toBe(2))
}, 15_000)
