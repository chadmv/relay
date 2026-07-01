import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { useState } from 'react'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { useTaskLogs } from './useTaskLogs'

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

test('does not fetch when disabled', async () => {
  let count = 0
  server.use(
    http.get('/v1/tasks/t1/logs', () => {
      count++
      return HttpResponse.json({ items: [], next_seq: 0, total: 0 })
    }),
  )
  renderHook(() => useTaskLogs('t1', false), { wrapper })
  await new Promise((r) => setTimeout(r, 50))
  expect(count).toBe(0)
})

test('fetches once when enabled and does not poll', async () => {
  let count = 0
  server.use(
    http.get('/v1/tasks/t1/logs', () => {
      count++
      return HttpResponse.json({
        items: [{ seq: 1, stream: 'stdout', content: 'hi', created_at: '2026-07-01T00:00:00Z' }],
        next_seq: 0,
        total: 1,
      })
    }),
  )
  const { result } = renderHook(() => useTaskLogs('t1', true), { wrapper })
  await waitFor(() => expect(result.current.data?.total).toBe(1))
  await new Promise((r) => setTimeout(r, 60))
  expect(count).toBe(1)
})

test('re-enabling after disable reuses the cached page instead of refetching', async () => {
  let count = 0
  server.use(
    http.get('/v1/tasks/t1/logs', () => {
      count++
      return HttpResponse.json({
        items: [{ seq: 1, stream: 'stdout', content: 'hi', created_at: '2026-07-01T00:00:00Z' }],
        next_seq: 0,
        total: 1,
      })
    }),
  )

  function Harness() {
    const [enabled, setEnabled] = useState(true)
    const { data } = useTaskLogs('t1', enabled)
    return (
      <div>
        <span data-testid="total">{data?.total ?? 'none'}</span>
        <button onClick={() => setEnabled((v) => !v)}>toggle</button>
      </div>
    )
  }

  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const { getByText, getByTestId } = render(
    <QueryClientProvider client={client}>
      <Harness />
    </QueryClientProvider>,
  )

  await waitFor(() => expect(getByTestId('total').textContent).toBe('1'))

  // toggle enabled false -> true -> false -> true
  fireEvent.click(getByText('toggle'))
  fireEvent.click(getByText('toggle'))
  fireEvent.click(getByText('toggle'))

  await waitFor(() => expect(getByTestId('total').textContent).toBe('1'))
  await new Promise((r) => setTimeout(r, 60))

  expect(count).toBe(1)
})
