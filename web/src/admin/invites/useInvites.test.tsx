import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../../test/setup-helpers'
import { useInvites } from './useInvites'
import type { InvitesPage } from './api'

const ROW = {
  id: 'i1',
  created_at: '2026-08-01T09:00:00Z',
  expires_at: '2026-08-04T09:00:00Z',
  created_by: 'u1',
  created_by_email: 'admin@studio.dev',
}

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

test('caches under ["invites", sort, cursor] and passes both through', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/invites', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [ROW], next_cursor: '', total: 1 })
    }),
  )
  const client = newClient()
  const { result } = renderHook(() => useInvites('expires_at', 'cur1'), {
    wrapper: makeWrapper(client),
  })

  await waitFor(() => expect(result.current.status).toBe('success'))
  expect(params?.get('sort')).toBe('expires_at')
  expect(params?.get('cursor')).toBe('cur1')
  expect(params?.get('limit')).toBe('50')

  const cached = client.getQueryData<InvitesPage>(['invites', 'expires_at', 'cur1'])
  expect(cached?.items[0].id).toBe('i1')
})

test('does not poll - invites are not live data', async () => {
  let calls = 0
  server.use(
    http.get('/v1/invites', () => {
      calls++
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  const { result } = renderHook(() => useInvites('-created_at', ''), {
    wrapper: makeWrapper(newClient()),
  })
  await waitFor(() => expect(result.current.status).toBe('success'))
  expect(calls).toBe(1)

  // Long enough that a copy-pasted refetchInterval (the live list hooks use
  // 3000ms, but 150ms catches any small value too) would have fired.
  await new Promise((r) => setTimeout(r, 150))
  expect(calls).toBe(1)

  // Positive control on the SAME counter: the instrument can move, so the
  // assertion above is about polling and not about a dead counter.
  await result.current.refetch()
  await waitFor(() => expect(calls).toBe(2))
})
