import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../../test/setup-helpers'
import { useAdminUsers } from './useAdminUsers'
import type { AdminUsersPage } from './api'

const USER = {
  id: 'u1',
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

test('caches under ["users", sort, includeArchived, cursor, email] and passes the params through', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/users', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [USER], next_cursor: '', total: 1 })
    }),
  )
  const client = newClient()
  const { result } = renderHook(() => useAdminUsers('name', true, 'cur1', 'a@b.co'), {
    wrapper: makeWrapper(client),
  })

  await waitFor(() => expect(result.current.status).toBe('success'))
  expect(params?.get('sort')).toBe('name')
  expect(params?.get('include_archived')).toBe('true')
  expect(params?.get('cursor')).toBe('cur1')
  expect(params?.get('email')).toBe('a@b.co')

  const cached = client.getQueryData<AdminUsersPage>(['users', 'name', true, 'cur1', 'a@b.co'])
  expect(cached?.items[0].id).toBe('u1')
})

test('does not poll - the users table is not live data', async () => {
  let calls = 0
  server.use(
    http.get('/v1/users', () => {
      calls++
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  const { result } = renderHook(() => useAdminUsers('-created_at', false, '', ''), {
    wrapper: makeWrapper(newClient()),
  })
  await waitFor(() => expect(result.current.status).toBe('success'))
  expect(calls).toBe(1)
  // Long enough that any accidental refetchInterval (the shipped list hooks use
  // 3000ms, and a copy-paste of useWorkers would inherit it) would have fired.
  await new Promise((r) => setTimeout(r, 120))
  expect(calls).toBe(1)
})
