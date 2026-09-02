import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { useJobs } from './useJobs'

// The client is built ONCE and closed over, not per render: a wrapper that
// constructs one in its body gives every rerender a fresh cache, so the flip
// below would measure a remount rather than the gate opening.
function makeWrapper(client: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>
  }
}

test('issues no request while disabled, and starts once enabled flips', async () => {
  let calls = 0
  server.use(
    http.get('/v1/jobs', () => {
      calls++
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  const { rerender } = renderHook(({ on }: { on: boolean }) => useJobs('-created_at', '', '', 20, on), {
    wrapper: makeWrapper(new QueryClient({ defaultOptions: { queries: { retry: false } } })),
    initialProps: { on: false },
  })

  // Two refetch intervals of real time; the interval is what would produce a
  // request if the gate leaked.
  await new Promise((r) => setTimeout(r, 120))
  expect(calls).toBe(0)

  // The control: without it, the assertion above passes on a harness that could
  // never observe a request at all.
  rerender({ on: true })
  await waitFor(() => expect(calls).toBeGreaterThanOrEqual(1))
})
