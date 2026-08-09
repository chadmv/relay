import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../../test/setup-helpers'
import { useWorkerOptions, WORKER_PICKER_LIMIT } from './useWorkerOptions'
import type { WorkersPage } from '../../workers/api'

const W = { id: 'w1', name: 'render-01', status: 'online' }

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

test('requests the first 200 workers by name and caches under ["workers","reservation-picker"]', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/workers', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [W], next_cursor: '', total: 1 })
    }),
  )
  const client = newClient()
  const { result } = renderHook(() => useWorkerOptions(), { wrapper: makeWrapper(client) })
  await waitFor(() => expect(result.current.status).toBe('success'))

  expect(WORKER_PICKER_LIMIT).toBe(200)
  expect(params?.get('limit')).toBe('200')
  expect(params?.get('sort')).toBe('name')
  // Under the BARE ['workers'] prefix, so the shipped worker mutations
  // (web/src/workers/useWorkerActions.ts:26, :50, :73, :82) invalidate it for free;
  // and 'reservation-picker' is not a WorkerSort, so it cannot collide with
  // useWorkers' ['workers', sort] key.
  const cached = client.getQueryData<WorkersPage>(['workers', 'reservation-picker'])
  expect(cached?.items[0].name).toBe('render-01')
})

test('does not poll - a form does not need the workers page 3s refresh', async () => {
  let calls = 0
  server.use(
    http.get('/v1/workers', () => {
      calls++
      return HttpResponse.json({ items: [W], next_cursor: '', total: 1 })
    }),
  )
  const { result } = renderHook(() => useWorkerOptions(), { wrapper: makeWrapper(newClient()) })
  await waitFor(() => expect(result.current.status).toBe('success'))
  expect(calls).toBe(1)

  // useWorkers polls at 3000ms; 150ms catches any small copy-pasted interval too.
  await new Promise((r) => setTimeout(r, 150))
  expect(calls).toBe(1)

  // Positive control on the SAME counter, so the equality above is about polling and
  // not about a dead instrument.
  await result.current.refetch()
  await waitFor(() => expect(calls).toBe(2))
})
