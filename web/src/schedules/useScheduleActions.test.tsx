import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test, vi } from 'vitest'
import { server } from '../test/setup-helpers'
import { useScheduleActions } from './useScheduleActions'
import { useSchedule } from './useSchedule'

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

test('runNow POSTs run-now and invalidates the schedules query', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(
    http.post('/v1/scheduled-jobs/s1/run-now', () => HttpResponse.json({ id: 'job1' }, { status: 201 })),
  )

  const { result } = renderHook(() => useScheduleActions(), { wrapper: makeWrapper(client) })
  await result.current.runNow.mutateAsync('s1')

  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['schedules'] }))
})

test('setEnabled PATCHes and invalidates the schedules query', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(
    http.patch('/v1/scheduled-jobs/s1', () => HttpResponse.json({ id: 's1', enabled: false })),
  )

  const { result } = renderHook(() => useScheduleActions(), { wrapper: makeWrapper(client) })
  await result.current.setEnabled.mutateAsync({ id: 's1', enabled: false })

  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['schedules'] }))
})

test('update PATCHes the patch verbatim and invalidates the BARE ["schedules"] prefix', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const spy = vi.spyOn(client, 'invalidateQueries')
  let body: unknown
  server.use(
    http.patch('/v1/scheduled-jobs/s1', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ id: 's1', cron_expr: '@every 30m' })
    }),
  )

  const { result } = renderHook(() => useScheduleActions(), { wrapper: makeWrapper(client) })
  await result.current.update.mutateAsync({ id: 's1', patch: { cron_expr: '@every 30m' } })

  // The hook must be a pass-through: it must not merge, default or "helpfully" add
  // fields. Any key it adds here recomputes next_run_at server-side.
  expect(body).toEqual({ cron_expr: '@every 30m' })
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['schedules'] }))
  // Every invalidation call must use the BARE prefix. A fully-qualified key would
  // only refresh the one sort/page combination that happens to be mounted.
  for (const call of spy.mock.calls) {
    expect((call[0] as { queryKey: unknown[] }).queryKey).toEqual(['schedules'])
  }
})

test('remove DELETEs the id, resolves on the empty 204, and invalidates the bare prefix', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const spy = vi.spyOn(client, 'invalidateQueries')
  let path: string | undefined
  server.use(
    http.delete('/v1/scheduled-jobs/s1', ({ request }) => {
      path = new URL(request.url).pathname
      return new HttpResponse(null, { status: 204 })
    }),
  )

  const { result } = renderHook(() => useScheduleActions(), { wrapper: makeWrapper(client) })
  await result.current.remove.mutateAsync('s1')

  expect(path).toBe('/v1/scheduled-jobs/s1')
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['schedules'] }))
})

test('update refetches a MOUNTED detail query (active observer, not a cache seed)', async () => {
  let detailCalls = 0
  server.use(
    http.get('/v1/scheduled-jobs/s1', () => {
      detailCalls++
      return HttpResponse.json({ id: 's1', name: 'nightly', owner_email: '', cron_expr: '0 2 * * *', timezone: 'UTC', job_spec: {}, overlap_policy: 'skip', enabled: true, next_run_at: '2099-01-01T00:00:00Z', created_at: '2026-06-01T00:00:00Z', updated_at: '2026-06-01T00:00:00Z' })
    }),
    http.patch('/v1/scheduled-jobs/s1', () => HttpResponse.json({ id: 's1' })),
  )
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const wrapper = makeWrapper(client)

  // The detail query MUST be mounted via renderHook so it has an ACTIVE OBSERVER.
  // A client.fetchQuery / setQueryData seed leaves no observer, invalidateQueries'
  // default refetchType:'active' never fires, and this assertion would pass
  // vacuously no matter which key the mutation invalidated. A long polling interval
  // is injected so the increment below can only come from the invalidation.
  const { result: detail } = renderHook(() => useSchedule('s1', 600_000), { wrapper })
  await waitFor(() => expect(detail.current.status).toBe('success'))
  expect(detailCalls).toBe(1)

  const { result: actions } = renderHook(() => useScheduleActions(), { wrapper })
  await actions.current.update.mutateAsync({ id: 's1', patch: { overlap_policy: 'allow' } })

  await waitFor(() => expect(detailCalls).toBeGreaterThanOrEqual(2))
})
