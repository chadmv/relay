import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test, vi } from 'vitest'
import { server } from '../test/setup-helpers'
import { useJobActions } from './useJobActions'

const ID = 'j1'

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

test('graceful cancel DELETEs /jobs/{id} with no force query string', async () => {
  const client = newClient()
  let search = ''
  server.use(
    http.delete(`/v1/jobs/${ID}`, ({ request }) => {
      search = new URL(request.url).search
      return HttpResponse.json({ id: ID, status: 'cancelled' })
    }),
  )
  const { result } = renderHook(() => useJobActions(ID), { wrapper: makeWrapper(client) })
  await result.current.cancel.mutateAsync(false)

  expect(search).toBe('')
})

test('force cancel DELETEs /jobs/{id}?force=true', async () => {
  const client = newClient()
  let force: string | null = null
  server.use(
    http.delete(`/v1/jobs/${ID}`, ({ request }) => {
      force = new URL(request.url).searchParams.get('force')
      return HttpResponse.json({ id: ID, status: 'cancelled' })
    }),
  )
  const { result } = renderHook(() => useJobActions(ID), { wrapper: makeWrapper(client) })
  await result.current.cancel.mutateAsync(true)

  expect(force).toBe('true')
})

test('onSuccess invalidates all THREE keys: [job,id], [jobs], and [job-stats]', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(http.delete(`/v1/jobs/${ID}`, () => HttpResponse.json({ id: ID, status: 'cancelled' })))
  const { result } = renderHook(() => useJobActions(ID), { wrapper: makeWrapper(client) })
  await result.current.cancel.mutateAsync(false)

  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['job', ID] }))
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['jobs'] }))
  // The decoupled stats key MUST be invalidated explicitly; ['jobs'] alone does
  // not reach ['job-stats'] (see queryKeyDecoupling.test.tsx). Missing this call
  // is the two-key regression.
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['job-stats'] }))
})

test('a failed cancel rejects and does not invalidate', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(
    http.delete(`/v1/jobs/${ID}`, () =>
      HttpResponse.json({ error: 'job is already in a terminal state' }, { status: 409 }),
    ),
  )
  const { result } = renderHook(() => useJobActions(ID), { wrapper: makeWrapper(client) })

  await expect(result.current.cancel.mutateAsync(false)).rejects.toBeTruthy()
  expect(spy).not.toHaveBeenCalledWith({ queryKey: ['job', ID] })
})
