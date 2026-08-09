import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test, vi } from 'vitest'
import { server } from '../../test/setup-helpers'
import { useAgentEnrollmentActions } from './useAgentEnrollmentActions'
import { useAgentEnrollments } from './useAgentEnrollments'

const TOKEN = 'f00dcafe'.repeat(8)

const ROW = {
  id: 'e1',
  created_at: '2026-08-09T00:00:00Z',
  expires_at: '2026-08-10T00:00:00Z',
  created_by: 'u1',
}

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

test('create POSTs the exact body and invalidates the BARE ["agent-enrollments"] prefix', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  let body: unknown
  server.use(
    http.post('/v1/agent-enrollments', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ id: 'e9', token: TOKEN, expires_at: ROW.expires_at }, { status: 201 })
    }),
  )
  const { result } = renderHook(() => useAgentEnrollmentActions(), { wrapper: makeWrapper(client) })
  const created = await result.current.create.mutateAsync({
    hostname_hint: 'farm-west-13',
    ttl_seconds: 86400,
  })

  expect(body).toEqual({ hostname_hint: 'farm-west-13', ttl_seconds: 86400 })
  expect(created.token).toBe(TOKEN)
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['agent-enrollments'] }))

  // The decoupling lesson from web/src/jobs/queryKeyDecoupling.test.tsx: a
  // fully-qualified key only refetches the sort/page combination that happens to
  // be mounted. EVERY call must use the bare prefix.
  for (const call of spy.mock.calls) {
    expect((call[0] as { queryKey: unknown[] }).queryKey).toEqual(['agent-enrollments'])
  }
})

test('creating refetches a MOUNTED enrollments list (active observer, not a cache seed)', async () => {
  let listCalls = 0
  server.use(
    http.get('/v1/agent-enrollments', () => {
      listCalls++
      return HttpResponse.json({ items: [ROW], next_cursor: '', total: 1 })
    }),
    http.post('/v1/agent-enrollments', () =>
      HttpResponse.json({ id: 'e9', token: TOKEN, expires_at: ROW.expires_at }, { status: 201 }),
    ),
  )
  const client = newClient()
  const wrapper = makeWrapper(client)

  // The list query MUST be mounted via renderHook so it has an ACTIVE OBSERVER.
  // A client.fetchQuery / setQueryData seed leaves no observer, invalidateQueries'
  // default refetchType:'active' never fires, and this assertion would pass
  // vacuously no matter what key the mutation invalidated.
  const { result: list } = renderHook(() => useAgentEnrollments('-created_at', ''), { wrapper })
  await waitFor(() => expect(list.current.status).toBe('success'))
  expect(listCalls).toBe(1)

  const { result: actions } = renderHook(() => useAgentEnrollmentActions(), { wrapper })
  await actions.current.create.mutateAsync({ ttl_seconds: 86400 })

  await waitFor(() => expect(listCalls).toBeGreaterThanOrEqual(2))
})

test('a create failure surfaces the ApiError and does not invalidate', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(
    http.post('/v1/agent-enrollments', () =>
      HttpResponse.json({ error: 'failed to create enrollment' }, { status: 500 }),
    ),
  )
  const { result } = renderHook(() => useAgentEnrollmentActions(), { wrapper: makeWrapper(client) })
  await expect(result.current.create.mutateAsync({ ttl_seconds: 86400 })).rejects.toMatchObject({
    status: 500,
  })
  expect(spy).not.toHaveBeenCalled()
})
