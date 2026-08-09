import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test, vi } from 'vitest'
import { server } from '../../test/setup-helpers'
import { useReservationActions } from './useReservationActions'
import { useReservations } from './useReservations'

const ROW = {
  id: 'r1',
  name: 'gpu-farm-hold',
  selector: null,
  worker_ids: ['11111111-1111-1111-1111-111111111111'],
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

// Typed structurally rather than as ReturnType<typeof vi.spyOn>: vi.spyOn's return
// type is generic over the spied method's own overloads, and QueryClient.invalidateQueries
// is itself generic, so binding this helper to the concrete spy type it is actually
// called with is all that is needed here - and it is what keeps tsc -b (the production
// build's type-check, which is stricter than vitest's transpile-only runtime) green.
function expectAllBarePrefix(spy: { mock: { calls: unknown[][] } }) {
  // The decoupling lesson from web/src/jobs/queryKeyDecoupling.test.tsx: a
  // fully-qualified key only refetches the sort/page combination that happens to be
  // mounted. EVERY call must use the bare prefix.
  for (const call of spy.mock.calls) {
    expect((call[0] as { queryKey: unknown[] }).queryKey).toEqual(['reservations'])
  }
}

test('create POSTs the exact body and invalidates the BARE ["reservations"] prefix', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  let body: unknown
  server.use(
    http.post('/v1/reservations', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ ...ROW, id: 'r9' }, { status: 201 })
    }),
  )
  const { result } = renderHook(() => useReservationActions(), { wrapper: makeWrapper(client) })
  const created = await result.current.create.mutateAsync({
    name: 'gpu-farm-hold',
    worker_ids: ROW.worker_ids,
  })

  expect(body).toEqual({ name: 'gpu-farm-hold', worker_ids: ROW.worker_ids })
  expect(created.id).toBe('r9')
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['reservations'] }))
  expectAllBarePrefix(spy)
})

test('creating refetches a MOUNTED reservations list (active observer, not a cache seed)', async () => {
  let listCalls = 0
  server.use(
    http.get('/v1/reservations', () => {
      listCalls++
      return HttpResponse.json({ items: [ROW], next_cursor: '', total: 1 })
    }),
    http.post('/v1/reservations', () => HttpResponse.json({ ...ROW, id: 'r9' }, { status: 201 })),
  )
  const client = newClient()
  const wrapper = makeWrapper(client)

  // The list query MUST be mounted via renderHook so it has an ACTIVE OBSERVER. A
  // client.fetchQuery / setQueryData seed leaves no observer, invalidateQueries'
  // default refetchType:'active' never fires, and this assertion would pass
  // vacuously no matter what key the mutation invalidated.
  const { result: list } = renderHook(() => useReservations('-created_at', ''), { wrapper })
  await waitFor(() => expect(list.current.status).toBe('success'))
  expect(listCalls).toBe(1)

  const { result: actions } = renderHook(() => useReservationActions(), { wrapper })
  await actions.current.create.mutateAsync({ name: 'x', worker_ids: ROW.worker_ids })

  await waitFor(() => expect(listCalls).toBeGreaterThanOrEqual(2))
})

test('remove DELETEs the id, resolves on the empty 204, and refetches the mounted list', async () => {
  let listCalls = 0
  let deletedPath: string | undefined
  server.use(
    http.get('/v1/reservations', () => {
      listCalls++
      return HttpResponse.json({ items: [ROW], next_cursor: '', total: 1 })
    }),
    http.delete('/v1/reservations/:id', ({ request }) => {
      deletedPath = new URL(request.url).pathname
      return new HttpResponse(null, { status: 204 })
    }),
  )
  const client = newClient()
  const wrapper = makeWrapper(client)
  const { result: list } = renderHook(() => useReservations('-created_at', ''), { wrapper })
  await waitFor(() => expect(list.current.status).toBe('success'))
  expect(listCalls).toBe(1)

  const { result: actions } = renderHook(() => useReservationActions(), { wrapper })
  await actions.current.remove.mutateAsync('r1')

  expect(deletedPath).toBe('/v1/reservations/r1')
  await waitFor(() => expect(listCalls).toBeGreaterThanOrEqual(2))
})

test('a remove 404 rejects AND still refetches, so the stale row leaves the table', async () => {
  // The interesting failure: someone else deleted the row first. onSettled (not
  // onSuccess) is what makes the error informational rather than a dead end.
  let listCalls = 0
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(
    http.get('/v1/reservations', () => {
      listCalls++
      return HttpResponse.json({ items: [ROW], next_cursor: '', total: 1 })
    }),
    http.delete('/v1/reservations/:id', () =>
      HttpResponse.json({ error: 'reservation not found' }, { status: 404 }),
    ),
  )
  const wrapper = makeWrapper(client)
  const { result: list } = renderHook(() => useReservations('-created_at', ''), { wrapper })
  await waitFor(() => expect(list.current.status).toBe('success'))
  expect(listCalls).toBe(1)

  const { result: actions } = renderHook(() => useReservationActions(), { wrapper })
  await expect(actions.current.remove.mutateAsync('gone')).rejects.toMatchObject({ status: 404 })

  await waitFor(() => expect(listCalls).toBeGreaterThanOrEqual(2))
  expectAllBarePrefix(spy)
})

test('a create failure surfaces the ApiError and does NOT invalidate (nothing was created)', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(
    http.post('/v1/reservations', () =>
      HttpResponse.json({ error: 'create reservation failed' }, { status: 500 }),
    ),
  )
  const { result } = renderHook(() => useReservationActions(), { wrapper: makeWrapper(client) })
  await expect(
    result.current.create.mutateAsync({ name: 'x', worker_ids: [] }),
  ).rejects.toMatchObject({ status: 500 })
  expect(spy).not.toHaveBeenCalled()
})
