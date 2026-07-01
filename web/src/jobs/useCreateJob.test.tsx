import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test, vi } from 'vitest'
import { server } from '../test/setup-helpers'
import { useCreateJob } from './useCreateJob'
import { useJobStats } from './useJobStats'

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

test('createJob POSTs the spec and returns the created job', async () => {
  const client = newClient()
  let body: unknown = null
  server.use(
    http.post('/v1/jobs', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ id: 'job-1', name: 'my-job', status: 'pending' }, { status: 201 })
    }),
  )
  const { result } = renderHook(() => useCreateJob(), { wrapper: makeWrapper(client) })
  const spec = { name: 'my-job', tasks: [{ name: 't', command: ['echo'] }] }
  const job = await result.current.mutateAsync(spec)

  expect(body).toEqual(spec)
  expect(job.id).toBe('job-1')
})

test('onSuccess invalidates BOTH ["jobs"] and ["job-stats"]', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(
    http.post('/v1/jobs', () =>
      HttpResponse.json({ id: 'job-1', name: 'my-job', status: 'pending' }, { status: 201 }),
    ),
  )
  const { result } = renderHook(() => useCreateJob(), { wrapper: makeWrapper(client) })
  await result.current.mutateAsync({ name: 'my-job', tasks: [{ name: 't' }] })

  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['jobs'] }))
  // The decoupled stats key MUST be invalidated explicitly; ['jobs'] alone does
  // not reach ['job-stats'] (see queryKeyDecoupling.test.tsx).
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['job-stats'] }))
})

test('the create refetches an ACTIVE ["job-stats"] observer', async () => {
  const client = newClient()
  let statsCalls = 0
  const bigInterval = 100_000 // never auto-refetch during the test
  server.use(
    http.get('/v1/jobs/stats', () => {
      statsCalls++
      return HttpResponse.json({ running: 0, queued: 0, done_24h: 0, failed_24h: 0 })
    }),
    http.post('/v1/jobs', () =>
      HttpResponse.json({ id: 'job-1', name: 'my-job', status: 'pending' }, { status: 201 }),
    ),
  )
  const wrapper = makeWrapper(client)

  // Mount a REAL stats observer so an invalidation triggers a refetch. A bare
  // fetchQuery seed would leave no observer and make the refetch un-observable.
  const stats = renderHook(() => useJobStats(bigInterval), { wrapper })
  await waitFor(() => expect(stats.result.current.status).toBe('success'))
  expect(statsCalls).toBe(1)

  const create = renderHook(() => useCreateJob(), { wrapper })
  await create.result.current.mutateAsync({ name: 'my-job', tasks: [{ name: 't' }] })

  // The active observer must refetch on invalidation: at least 2 total hits.
  await waitFor(() => expect(statsCalls).toBeGreaterThanOrEqual(2))
})

test('a failed create rejects and does not invalidate', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(
    http.post('/v1/jobs', () =>
      HttpResponse.json({ error: 'duplicate task name: build' }, { status: 400 }),
    ),
  )
  const { result } = renderHook(() => useCreateJob(), { wrapper: makeWrapper(client) })

  await expect(
    result.current.mutateAsync({ name: 'x', tasks: [{ name: 't' }] }),
  ).rejects.toBeTruthy()
  expect(spy).not.toHaveBeenCalledWith({ queryKey: ['jobs'] })
})
