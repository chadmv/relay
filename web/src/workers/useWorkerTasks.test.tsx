import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { afterEach, expect, test, vi } from 'vitest'
import { server } from '../test/setup-helpers'
import { useWorkerTasks } from './useWorkerTasks'

const EMPTY = { items: [], next_cursor: '', total: 0 }

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

afterEach(() => vi.useRealTimers())

test('caches under ["worker", id, "tasks"] and exposes the page', async () => {
  server.use(
    http.get('/v1/workers/w1/tasks', () =>
      HttpResponse.json({ items: [], next_cursor: '', total: 3 }),
    ),
  )
  const client = newClient()
  const { result } = renderHook(() => useWorkerTasks('w1'), { wrapper: makeWrapper(client) })
  await waitFor(() => expect(result.current.status).toBe('success'))
  expect(client.getQueryData(['worker', 'w1', 'tasks'])).toEqual({
    items: [],
    next_cursor: '',
    total: 3,
  })
})

test('an injected interval drives the poll', async () => {
  let calls = 0
  server.use(
    http.get('/v1/workers/w1/tasks', () => {
      calls++
      return HttpResponse.json(EMPTY)
    }),
  )
  renderHook(() => useWorkerTasks('w1', { intervalMs: 20 }), { wrapper: makeWrapper(newClient()) })
  await waitFor(() => expect(calls).toBeGreaterThanOrEqual(2))
})

test('polls on the DEFAULT 3s worker cadence, and not before', async () => {
  // Behavioral, not constant-reading: a test that imports an exported constant
  // proves nothing about what the hook passes to refetchInterval. The call
  // counter is its own positive control, so the equality below is about the
  // interval and not about a dead instrument. 2.5s then 1s discriminates
  // against any cadence above 3s.
  vi.useFakeTimers({ shouldAdvanceTime: true })
  let calls = 0
  server.use(
    http.get('/v1/workers/w1/tasks', () => {
      calls++
      return HttpResponse.json(EMPTY)
    }),
  )
  const { result } = renderHook(() => useWorkerTasks('w1'), { wrapper: makeWrapper(newClient()) })
  await waitFor(() => expect(result.current.status).toBe('success'))
  expect(calls).toBe(1)

  await act(async () => {
    vi.advanceTimersByTime(2_500)
  })
  expect(calls).toBe(1)

  await act(async () => {
    vi.advanceTimersByTime(1_000)
  })
  await waitFor(() => expect(calls).toBeGreaterThanOrEqual(2))
})
