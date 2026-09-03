import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { afterEach, expect, test, vi } from 'vitest'
import { server } from '../test/setup-helpers'
import { useJobTimeline } from './useJobTimeline'
import { ANCHOR_STEP_MS } from './timelineWindow'

afterEach(() => vi.useRealTimers())

// Hand-written wire bodies, never marshalled through the api types.
function jobRow(id: string, name: string) {
  return {
    id,
    name,
    priority: 'normal',
    status: 'pending',
    submitted_by_email: 'a@x.dev',
    labels: null,
    created_at: '2026-09-02T10:00:00Z',
    updated_at: '2026-09-02T10:00:00Z',
    total_tasks: 1,
    done_tasks: 0,
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

test('the walk repeats its filters on every page', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    http.get('/v1/jobs', ({ request }) => {
      const p = new URL(request.url).searchParams
      seen.push(p)
      const cursor = p.get('cursor') ?? ''
      return HttpResponse.json({
        items: [jobRow(`ID${seen.length}`, `job-${seen.length}`)],
        next_cursor: cursor === '' ? 'CUR1' : '',
        total: 2,
      })
    }),
  )

  const { result } = renderHook(() => useJobTimeline(true, '24h', 'etl', true), {
    wrapper: makeWrapper(newClient()),
  })
  await waitFor(() => expect(result.current.jobs).toHaveLength(2))

  const since = seen[0].get('since')
  const until = seen[0].get('until')
  expect(since).toBeTruthy()
  expect(until).toBeTruthy()
  // Page two must carry the IDENTICAL window and filters. Sending them on page
  // one only leaves the rest of the walk reading a different set, under a label
  // describing the first.
  for (const p of seen) {
    expect(p.get('since')).toBe(since)
    expect(p.get('until')).toBe(until)
    expect(p.get('q')).toBe('etl')
    expect(p.get('mine')).toBe('true')
    expect(p.get('limit')).toBe('200')
    expect(p.has('sort')).toBe(false)
    expect(p.has('status')).toBe(false)
  }
  expect(seen[0].has('cursor')).toBe(false)
  expect(seen[1].get('cursor')).toBe('CUR1')
})

test('a walk slower than one anchor tick still completes, and the tick does not restart it', async () => {
  // Reproduces the liveness bug: the old design derived the query key from a
  // ticking clock (useNow), so every ANCHOR_STEP_MS the key changed, the
  // in-flight walk's query went inactive, and the walk restarted from page 1 -
  // forever, for any walk slower than one tick. The fix makes the key STABLE
  // per window and filters and lets TanStack's own refetchInterval drive the
  // refresh, which dedupes against a fetch already in flight.
  vi.useFakeTimers({ shouldAdvanceTime: true })
  let requests = 0
  let release!: () => void
  const gate = new Promise<void>((r) => {
    release = r
  })
  server.use(
    http.get('/v1/jobs', async () => {
      requests++
      await gate
      return HttpResponse.json({ items: [jobRow('AAAAAA', 'job-A')], next_cursor: '', total: 1 })
    }),
  )
  const { result } = renderHook(() => useJobTimeline(true, '24h', '', false), {
    wrapper: makeWrapper(newClient()),
  })
  await waitFor(() => expect(requests).toBe(1))
  expect(result.current.isLoading).toBe(true)

  // Four ticks pass while the single request is still pending. Under the old
  // design this is where four abandoned requests would appear and isLoading
  // would still read true with nothing ever completing.
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ANCHOR_STEP_MS * 4)
  })
  expect(requests).toBe(1)
  expect(result.current.isLoading).toBe(true)

  release()
  await waitFor(() => expect(result.current.isLoading).toBe(false))
  expect(result.current.jobs).toHaveLength(1)
})

test('the walk stops at the page cap', async () => {
  const seen: URLSearchParams[] = []
  server.use(
    http.get('/v1/jobs', ({ request }) => {
      seen.push(new URL(request.url).searchParams)
      // Every page offers another, so only the cap can stop this.
      return HttpResponse.json({
        items: [jobRow(`ID${seen.length}`, `job-${seen.length}`)],
        next_cursor: `CUR${seen.length}`,
        total: 999,
      })
    }),
  )

  const { result } = renderHook(() => useJobTimeline(true, '24h', '', false), {
    wrapper: makeWrapper(newClient()),
  })
  await waitFor(() => expect(result.current.truncated).toBe(true))

  // Filtered by the first request's anchor rather than counted raw: the anchor
  // advances on its own 15-second clock, and a run that straddled a step would
  // mint a second key and a second walk. Every page of THIS walk carries this
  // since.
  const since = seen[0].get('since')
  expect(seen.filter((p) => p.get('since') === since)).toHaveLength(3)
})

test('a window that drains while total grows is not truncated', async () => {
  let n = 0
  server.use(
    http.get('/v1/jobs', ({ request }) => {
      const cursor = new URL(request.url).searchParams.get('cursor') ?? ''
      n++
      // total GROWS between the two pages, because jobs are created while a walk
      // runs. drawn < total is therefore TRUE on a window that drained, and only
      // the last page's cursor can say whether anything was left behind.
      return HttpResponse.json({
        items: [jobRow(`ID${n}`, `job-${n}`)],
        next_cursor: cursor === '' ? 'CUR1' : '',
        total: cursor === '' ? 2 : 57,
      })
    }),
  )

  const { result } = renderHook(() => useJobTimeline(true, '24h', '', false), {
    wrapper: makeWrapper(newClient()),
  })
  await waitFor(() => expect(result.current.jobs).toHaveLength(2))
  expect(result.current.truncated).toBe(false)
  // The banner's denominator is the freshest number the walk saw.
  expect(result.current.total).toBe(57)
})

test('changing the window ends the previous walk', async () => {
  const seen: URLSearchParams[] = []
  let releasePage2!: () => void
  const page2Gate = new Promise<void>((r) => {
    releasePage2 = r
  })

  server.use(
    http.get('/v1/jobs', async ({ request }) => {
      const p = new URL(request.url).searchParams
      seen.push(p)
      const cursor = p.get('cursor') ?? ''
      if (cursor === 'CUR1') await page2Gate
      return HttpResponse.json({
        items: [jobRow(`ID${seen.length}`, `job-${seen.length}`)],
        next_cursor: cursor === '' ? 'CUR1' : 'CUR2',
        total: 999,
      })
    }),
  )

  const { rerender, result } = renderHook<ReturnType<typeof useJobTimeline>, { w: '24h' | '6h' }>(
    ({ w }) => useJobTimeline(true, w, '', false),
    { wrapper: makeWrapper(newClient()), initialProps: { w: '24h' } },
  )
  await waitFor(() => expect(seen.some((p) => p.get('cursor') === 'CUR1')).toBe(true))
  const oldSince = seen[0].get('since')

  rerender({ w: '6h' })
  await waitFor(() => expect(seen.some((p) => p.get('since') !== oldSince)).toBe(true))

  releasePage2()
  await waitFor(() => expect(result.current.jobs.length).toBeGreaterThan(0))
  // A bounded wait for an ABSENCE: the released page-2 response is what a walk
  // that never checked its signal would use to ask for page three, and that
  // request would land a tick after the assertion without this.
  await new Promise((r) => setTimeout(r, 80))

  // Two pages of the old window went out before the switch. A third means the
  // abandoned walk kept fetching under a key nothing renders, competing with the
  // live one for the browser's connections.
  expect(seen.filter((p) => p.get('since') === oldSince)).toHaveLength(2)
})

test('a failed refresh keeps the previous rows and reports no error', async () => {
  let fail = false
  server.use(
    http.get('/v1/jobs', () => {
      if (fail) return HttpResponse.json({ error: 'boom' }, { status: 500 })
      return HttpResponse.json({ items: [jobRow('AAAAAA', 'job-A')], next_cursor: '', total: 1 })
    }),
  )

  const client = newClient()
  const { result } = renderHook(() => useJobTimeline(true, '24h', '', false), {
    wrapper: makeWrapper(client),
  })
  await waitFor(() => expect(result.current.jobs).toHaveLength(1))

  fail = true
  await act(async () => {
    await client.refetchQueries({ queryKey: ['job-timeline'] })
  })
  // Mirrors the table view's error && !data rule: a failed refresh over existing
  // data keeps the data visible rather than blanking the chart.
  expect(result.current.jobs).toHaveLength(1)
  expect(result.current.error).toBeNull()
})

test('the walk issues no request while disabled', async () => {
  let calls = 0
  server.use(
    http.get('/v1/jobs', () => {
      calls++
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  renderHook(() => useJobTimeline(false, '24h', '', false), { wrapper: makeWrapper(newClient()) })
  await new Promise((r) => setTimeout(r, 80))
  expect(calls).toBe(0)
})
