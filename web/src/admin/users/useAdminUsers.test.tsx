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
  server.use(
    http.get('/v1/users', () => HttpResponse.json({ items: [], next_cursor: '', total: 0 })),
  )
  const client = newClient()
  const { result } = renderHook(() => useAdminUsers('-created_at', false, '', ''), {
    wrapper: makeWrapper(client),
  })
  await waitFor(() => expect(result.current.status).toBe('success'))

  // Deterministic: inspect the resolved query's own options rather than racing a
  // wall-clock timer against a 3000ms refetchInterval. A short wait (as this test
  // used to do) only proves nothing fired in that window, not that polling is
  // disabled - it stays green even if refetchInterval: 3000 is added below.
  // `Query.options` is publicly typed as the base QueryOptions (no
  // refetchInterval field), but useQuery merges the full observer options
  // (including refetchInterval) into the object it hands the cache at runtime -
  // hence the narrow cast rather than `as any`.
  const query = client
    .getQueryCache()
    .find({ queryKey: ['users', '-created_at', false, '', ''] })
  const options = query?.options as
    | { refetchInterval?: unknown; placeholderData?: unknown }
    | undefined
  // Positive control on the same path first: this assertion is a property *absence*,
  // so if TanStack ever stops merging observer options onto Query.options it would
  // silently re-green. placeholderData: keepPreviousData is set on this hook and
  // lands via the identical merge, so if it is missing the probe itself is broken
  // and the refetchInterval check below proves nothing.
  expect(options?.placeholderData).toBeDefined()
  expect(options?.refetchInterval).toBeUndefined()
})
