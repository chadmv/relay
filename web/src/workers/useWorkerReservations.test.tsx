import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { useWorkerReservations } from './useWorkerReservations'

function wrapperFor(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

// The middle element is the whole collision argument: the admin tab's key is
// ['reservations', sort, cursor] and 'worker' is not a member of ReservationSort,
// so the two cannot resolve to the same key. Checked, not assumed - a collision
// would silently serve one panel the other's page.
test('the query key carries the worker discriminator and the worker id', async () => {
  server.use(
    http.get('/v1/reservations', () =>
      HttpResponse.json({ items: [], next_cursor: '', total: 0 }),
    ),
  )
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const { result } = renderHook(() => useWorkerReservations('w-1'), { wrapper: wrapperFor(client) })
  await waitFor(() => expect(result.current.isSuccess).toBe(true))
  expect(client.getQueryCache().getAll().map((q) => q.queryKey)).toEqual([
    ['reservations', 'worker', 'w-1'],
  ])
})

// Kills: the hook forgetting to pass the id through, which would render every
// reservation in the fleet under this worker's name.
test('the request carries the worker id as the filter', async () => {
  let search: string | undefined
  server.use(
    http.get('/v1/reservations', ({ request }) => {
      search = new URL(request.url).search
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const { result } = renderHook(() => useWorkerReservations('w-9'), { wrapper: wrapperFor(client) })
  await waitFor(() => expect(result.current.isSuccess).toBe(true))
  expect(search).toBe('?sort=-created_at&limit=50&worker_id=w-9')
})

// Mounted through renderHook so there is an ACTIVE observer: invalidateQueries
// defaults to refetchType 'active', so a cache seeded without a mounted observer
// would refetch nothing and this test would pass against any key at all.
// Kills: a key outside the bare ['reservations'] prefix, which useReservationActions
// invalidates after an admin create or delete.
test('an admin action invalidating the bare reservations prefix refetches this panel', async () => {
  let calls = 0
  server.use(
    http.get('/v1/reservations', () => {
      calls++
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const { result } = renderHook(() => useWorkerReservations('w-1'), { wrapper: wrapperFor(client) })
  await waitFor(() => expect(result.current.isSuccess).toBe(true))
  expect(calls).toBe(1)
  await client.invalidateQueries({ queryKey: ['reservations'] })
  await waitFor(() => expect(calls).toBe(2))
})
