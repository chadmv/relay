import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test, vi } from 'vitest'
import { server } from '../test/setup-helpers'
import { useWorkerActions } from './useWorkerActions'
import type { Worker } from './api'

const ID = 'w1'

const WORKER: Worker = {
  id: ID,
  name: 'rig',
  hostname: 'h',
  cpu_cores: 8,
  ram_gb: 32,
  gpu_count: 0,
  gpu_model: '',
  os: 'linux',
  max_slots: 2,
  labels: null,
  status: 'online',
}

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

test('updateWorker PATCHes, writes the response into the cache, and invalidates worker + workers', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(
    http.patch(`/v1/workers/${ID}`, () => HttpResponse.json({ ...WORKER, name: 'renamed' })),
  )
  const { result } = renderHook(() => useWorkerActions(ID), { wrapper: makeWrapper(client) })
  await result.current.update.mutateAsync({ name: 'renamed' })

  expect((client.getQueryData(['worker', ID]) as Worker).name).toBe('renamed')
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['worker', ID] }))
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['workers'] }))
})

test('disable (requeue=false) POSTs /disable with no query string and invalidates', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  let seenUrl = ''
  server.use(
    http.post(`/v1/workers/${ID}/disable`, ({ request }) => {
      seenUrl = new URL(request.url).search
      return HttpResponse.json({ ...WORKER, status: 'disabled', disabled_at: 'now', requeued_tasks: 0 })
    }),
  )
  const { result } = renderHook(() => useWorkerActions(ID), { wrapper: makeWrapper(client) })
  await result.current.disable.mutateAsync(false)

  expect(seenUrl).toBe('')
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['workers'] }))
})

test('disable (requeue=true) POSTs /disable?requeue=true and returns requeued_tasks', async () => {
  const client = newClient()
  let seenUrl = ''
  server.use(
    http.post(`/v1/workers/${ID}/disable`, ({ request }) => {
      seenUrl = new URL(request.url).search
      return HttpResponse.json({ ...WORKER, status: 'disabled', disabled_at: 'now', requeued_tasks: 3 })
    }),
  )
  const { result } = renderHook(() => useWorkerActions(ID), { wrapper: makeWrapper(client) })
  const res = await result.current.disable.mutateAsync(true)

  expect(seenUrl).toBe('?requeue=true')
  expect(res.requeued_tasks).toBe(3)
})

test('enable POSTs /enable and invalidates', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(http.post(`/v1/workers/${ID}/enable`, () => HttpResponse.json(WORKER)))
  const { result } = renderHook(() => useWorkerActions(ID), { wrapper: makeWrapper(client) })
  await result.current.enable.mutateAsync()

  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['workers'] }))
})

test('revoke DELETEs /token, does NOT invalidate [worker,id], invalidates [workers]', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(http.delete(`/v1/workers/${ID}/token`, () => new HttpResponse(null, { status: 204 })))
  const { result } = renderHook(() => useWorkerActions(ID), { wrapper: makeWrapper(client) })
  await result.current.revoke.mutateAsync()

  expect(spy).not.toHaveBeenCalledWith({ queryKey: ['worker', ID] })
  expect(spy).toHaveBeenCalledWith({ queryKey: ['workers'] })
})

test('evict POSTs the evict path and invalidates the workspaces query', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  let seen = false
  server.use(
    http.post(`/v1/workers/${ID}/workspaces/ws-a/evict`, () => {
      seen = true
      return new HttpResponse(null, { status: 202 })
    }),
  )
  const { result } = renderHook(() => useWorkerActions(ID), { wrapper: makeWrapper(client) })
  await result.current.evict.mutateAsync('ws-a')

  expect(seen).toBe(true)
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['worker', ID, 'workspaces'] }))
})

test('disable optimistically flips cached status and rolls back on error', async () => {
  const client = newClient()
  client.setQueryData(['worker', ID], WORKER)
  server.use(
    http.post(`/v1/workers/${ID}/disable`, () => HttpResponse.json({ error: 'boom' }, { status: 500 })),
  )
  const { result } = renderHook(() => useWorkerActions(ID), { wrapper: makeWrapper(client) })

  await expect(result.current.disable.mutateAsync(false)).rejects.toBeTruthy()
  // After rollback the cached worker is back to its pre-mutation state.
  await waitFor(() => expect((client.getQueryData(['worker', ID]) as Worker).status).toBe('online'))
  expect((client.getQueryData(['worker', ID]) as Worker).disabled_at).toBeUndefined()
})
