import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../../test/setup-helpers'
import { useReservations } from './useReservations'
import type { ReservationsPage } from './api'

const ROW = {
  id: 'r1',
  name: 'gpu-farm-hold',
  selector: null,
  worker_ids: [],
  user_id: 'u1',
  created_at: '2026-08-09T09:30:00Z',
}

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

test('caches under ["reservations", sort, cursor] and passes both through', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/reservations', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [ROW], next_cursor: '', total: 1 })
    }),
  )
  const client = newClient()
  const { result } = renderHook(() => useReservations('starts_at', 'cur1'), {
    wrapper: makeWrapper(client),
  })
  await waitFor(() => expect(result.current.status).toBe('success'))

  expect(params?.get('sort')).toBe('starts_at')
  expect(params?.get('cursor')).toBe('cur1')
  expect(params?.get('limit')).toBe('50')
  const cached = client.getQueryData<ReservationsPage>(['reservations', 'starts_at', 'cur1'])
  expect(cached?.items[0].id).toBe('r1')
})

test('does not poll - reservations are not live data', async () => {
  let calls = 0
  server.use(
    http.get('/v1/reservations', () => {
      calls++
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  const { result } = renderHook(() => useReservations('-created_at', ''), {
    wrapper: makeWrapper(newClient()),
  })
  await waitFor(() => expect(result.current.status).toBe('success'))
  expect(calls).toBe(1)

  await new Promise((r) => setTimeout(r, 150))
  expect(calls).toBe(1)

  // Positive control on the SAME counter: the instrument can move, so the assertion
  // above is about polling and not about a dead counter.
  await result.current.refetch()
  await waitFor(() => expect(calls).toBe(2))
})
