import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test, vi } from 'vitest'
import { server } from '../../test/setup-helpers'
import { useAdminUserActions } from './useAdminUserActions'
import { useAdminUsers } from './useAdminUsers'

const ID = 'u1'

const USER = {
  id: ID,
  email: 'a@b.co',
  name: 'A',
  is_admin: false,
  created_at: '2026-08-01T00:00:00Z',
  archived_at: null,
}

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

test('create POSTs /users with the body and invalidates the bare ["users"] prefix', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  let body: unknown
  server.use(
    http.post('/v1/users', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json(USER, { status: 201 })
    }),
  )
  const { result } = renderHook(() => useAdminUserActions(), { wrapper: makeWrapper(client) })
  await result.current.create.mutateAsync({
    email: 'a@b.co',
    name: 'A',
    password: 'password1',
    is_admin: false,
  })

  expect(body).toEqual({ email: 'a@b.co', name: 'A', password: 'password1', is_admin: false })
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['users'] }))
})

test('rename PATCHes /users/{id} with only a name and invalidates ["users"]', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  let body: unknown
  server.use(
    http.patch(`/v1/users/${ID}`, async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ ...USER, name: 'Renamed' })
    }),
  )
  const { result } = renderHook(() => useAdminUserActions(), { wrapper: makeWrapper(client) })
  await result.current.rename.mutateAsync({ id: ID, name: 'Renamed' })

  expect(body).toEqual({ name: 'Renamed' })
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['users'] }))
})

test('archive POSTs /users/{id}/archive and invalidates ["users"]', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(
    http.post(`/v1/users/${ID}/archive`, () =>
      HttpResponse.json({ ...USER, archived_at: '2026-08-02T00:00:00Z' }),
    ),
  )
  const { result } = renderHook(() => useAdminUserActions(), { wrapper: makeWrapper(client) })
  await result.current.archive.mutateAsync(ID)

  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['users'] }))
})

test('unarchive POSTs /users/{id}/unarchive and invalidates ["users"]', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(http.post(`/v1/users/${ID}/unarchive`, () => HttpResponse.json(USER)))
  const { result } = renderHook(() => useAdminUserActions(), { wrapper: makeWrapper(client) })
  await result.current.unarchive.mutateAsync(ID)

  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['users'] }))
})

test('resetPassword POSTs email + new_password, resolves on a 204 with no body, and invalidates ["users"]', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  let body: unknown
  server.use(
    http.post('/v1/users/password-reset', async ({ request }) => {
      body = await request.json()
      return new HttpResponse(null, { status: 204 })
    }),
  )
  const { result } = renderHook(() => useAdminUserActions(), { wrapper: makeWrapper(client) })
  await expect(
    result.current.resetPassword.mutateAsync({ email: 'a@b.co', newPassword: 'password1' }),
  ).resolves.toBeUndefined()

  expect(body).toEqual({ email: 'a@b.co', new_password: 'password1' })
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['users'] }))
})

test('no mutation invalidates a fully-qualified list key', async () => {
  const client = newClient()
  const spy = vi.spyOn(client, 'invalidateQueries')
  server.use(http.post(`/v1/users/${ID}/archive`, () => HttpResponse.json(USER)))
  const { result } = renderHook(() => useAdminUserActions(), { wrapper: makeWrapper(client) })
  await result.current.archive.mutateAsync(ID)

  // The decoupling lesson from web/src/jobs/queryKeyDecoupling.test.tsx: a
  // fully-qualified key only refetches the page/sort/filter combination that
  // happens to be mounted. Every call must use the bare prefix.
  await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: ['users'] }))
  for (const call of spy.mock.calls) {
    const key = (call[0] as { queryKey: unknown[] }).queryKey
    expect(key).toEqual(['users'])
  }
})

test('archiving refetches a MOUNTED users list (active observer, not a cache seed)', async () => {
  let listCalls = 0
  server.use(
    http.get('/v1/users', () => {
      listCalls++
      return HttpResponse.json({ items: [USER], next_cursor: '', total: 1 })
    }),
    http.post(`/v1/users/${ID}/archive`, () =>
      HttpResponse.json({ ...USER, archived_at: '2026-08-02T00:00:00Z' }),
    ),
  )
  const client = newClient()
  const wrapper = makeWrapper(client)

  // The list query MUST be mounted via renderHook so it has an active observer.
  // A client.fetchQuery / setQueryData seed leaves no observer, invalidateQueries'
  // default refetchType:'active' never fires, and this assertion would pass
  // vacuously no matter what key the mutation invalidates.
  const { result: list } = renderHook(() => useAdminUsers('-created_at', false, '', ''), { wrapper })
  await waitFor(() => expect(list.current.status).toBe('success'))
  expect(listCalls).toBe(1)

  const { result: actions } = renderHook(() => useAdminUserActions(), { wrapper })
  await actions.current.archive.mutateAsync(ID)

  await waitFor(() => expect(listCalls).toBeGreaterThanOrEqual(2))
})
