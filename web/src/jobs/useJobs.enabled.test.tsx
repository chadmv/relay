import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { useJobs } from './useJobs'

// Sibling to useJobs.test.tsx, which is gate-frozen for this slice. The `enabled`
// parameter gets its own test with itself as the subject rather than a passing
// mention inside an existing one.
function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
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
    wrapper,
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
