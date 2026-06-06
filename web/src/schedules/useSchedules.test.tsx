import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { useSchedules } from './useSchedules'

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

test('fetches schedules and refetches on the interval', async () => {
  let count = 0
  server.use(
    http.get('/v1/scheduled-jobs', () => {
      count++
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )

  renderHook(() => useSchedules('-created_at', undefined, 20), { wrapper })

  await waitFor(() => expect(count).toBeGreaterThanOrEqual(1))
  await waitFor(() => expect(count).toBeGreaterThanOrEqual(2))
})

test('passes the cursor through to the request', async () => {
  let cursor: string | null = null
  server.use(
    http.get('/v1/scheduled-jobs', ({ request }) => {
      cursor = new URL(request.url).searchParams.get('cursor')
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )

  renderHook(() => useSchedules('-created_at', 'PAGE2', 20), { wrapper })
  await waitFor(() => expect(cursor).toBe('PAGE2'))
})
