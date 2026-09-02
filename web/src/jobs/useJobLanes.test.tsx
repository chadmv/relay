import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { useJobLanes } from './useJobLanes'

// Wire bodies are hand-written, never marshalled through JobsPage or api types: a
// fixture built from the production interface agrees with the decoder by
// construction and cannot detect drift in either direction.
function jobRow(id: string, name: string, status: string) {
  return {
    id,
    name,
    priority: 'normal',
    status,
    submitted_by_email: 'a@x.dev',
    labels: null,
    created_at: '2026-06-05T10:00:00Z',
    updated_at: '2026-06-05T10:00:00Z',
    total_tasks: 4,
    done_tasks: 2,
  }
}

function makeWrapper(client: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>
  }
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

test('each lane requests its own status at the cap and never sends sort or cursor', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    http.get('/v1/jobs', ({ request }) => {
      seen.push(new URL(request.url).searchParams)
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  renderHook(() => useJobLanes(true, 10, 100_000), { wrapper: makeWrapper(newClient()) })

  await waitFor(() => expect(seen).toHaveLength(5))
  expect(seen.map((p) => p.get('status')).sort()).toEqual([
    'cancelled',
    'done',
    'failed',
    'pending',
    'running',
  ])
  for (const p of seen) {
    expect(p.get('limit')).toBe('10')
    expect(p.has('sort')).toBe(false)
    expect(p.has('cursor')).toBe(false)
  }
})

test('the lanes issue no request while disabled', async () => {
  let calls = 0
  server.use(
    http.get('/v1/jobs', () => {
      calls++
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  const { result } = renderHook(() => useJobLanes(false, 10, 20), {
    wrapper: makeWrapper(newClient()),
  })
  // Two refetch intervals of real time. Asserting an absence needs a bounded wait,
  // and the interval is the thing that would produce a request if the gate leaked.
  await new Promise((r) => setTimeout(r, 120))
  expect(calls).toBe(0)
  // The lane structure still exists so the view can render its five columns.
  expect(result.current).toHaveLength(5)
  expect(result.current[0].status).toBe('pending')
  expect(result.current[0].items).toEqual([])
})

test('invalidating the jobs list does not refetch the lanes', async () => {
  let calls = 0
  server.use(
    http.get('/v1/jobs', () => {
      calls++
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  const client = newClient()
  renderHook(() => useJobLanes(true, 10, 100_000), { wrapper: makeWrapper(client) })
  await waitFor(() => expect(calls).toBe(5))

  // invalidateQueries awaits the refetch of every ACTIVE query it matches, so if
  // the lane keys sat under the 'jobs' prefix the counter would already have moved
  // by the time this resolves.
  await act(async () => {
    await client.invalidateQueries({ queryKey: ['jobs'] })
  })
  expect(calls).toBe(5)

  // The control: the same call against the lanes' own prefix MUST move it. Without
  // this, the assertion above passes equally on a hook that mounted nothing.
  await act(async () => {
    await client.invalidateQueries({ queryKey: ['job-lanes'] })
  })
  await waitFor(() => expect(calls).toBe(10))
})

test('a 500 on one lane leaves the other four with their rows', async () => {
  server.use(
    http.get('/v1/jobs', ({ request }) => {
      const status = new URL(request.url).searchParams.get('status') ?? ''
      if (status === 'failed') {
        return HttpResponse.json({ error: 'list jobs failed' }, { status: 500 })
      }
      return HttpResponse.json({
        items: [jobRow(`ID-${status}`, `job-${status}`, status)],
        next_cursor: '',
        total: 3,
      })
    }),
  )
  const { result } = renderHook(() => useJobLanes(true, 10, 100_000), {
    wrapper: makeWrapper(newClient()),
  })

  await waitFor(() => {
    expect(result.current.find((l) => l.status === 'failed')?.error).toBeTruthy()
    expect(result.current.filter((l) => l.status !== 'failed').every((l) => l.items.length === 1)).toBe(
      true,
    )
  })
  for (const lane of result.current.filter((l) => l.status !== 'failed')) {
    expect(lane.total).toBe(3)
    expect(lane.error).toBeNull()
    expect(lane.items[0].name).toBe(`job-${lane.status}`)
  }
})
