import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { afterEach, expect, test, vi } from 'vitest'
import { server } from '../test/setup-helpers'
import { SCHEDULE_RUNS_LIMIT, useScheduleRuns } from './useScheduleRuns'
import type { JobsPage } from '../jobs/api'

const JOB = {
  id: 'j1',
  name: 'nightly-build',
  priority: 'normal',
  status: 'done',
  labels: null,
  created_at: '2026-06-05T02:00:00Z',
  updated_at: '2026-06-05T02:04:00Z',
}

function makeWrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

function newClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

afterEach(() => vi.useRealTimers())

test('requests the latest 20 runs for the schedule with no sort, cached under ["schedules","runs",id]', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/jobs', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [JOB], next_cursor: '', total: 37 })
    }),
  )
  const client = newClient()
  const { result } = renderHook(() => useScheduleRuns('s1'), { wrapper: makeWrapper(client) })
  await waitFor(() => expect(result.current.status).toBe('success'))

  expect(SCHEDULE_RUNS_LIMIT).toBe(20)
  expect(params?.get('scheduled_job_id')).toBe('s1')
  expect(params?.get('limit')).toBe('20')
  expect(params?.has('sort')).toBe(false)
  const cached = client.getQueryData<JobsPage>(['schedules', 'runs', 's1'])
  expect(cached?.items[0].id).toBe('j1')
  // total is the FULL count from CountJobsByScheduledJob (jobs.sql:80-81), not the
  // page size: the footer says "latest 20 of 37", which is the honest claim for a
  // fixed window with no pager.
  expect(cached?.total).toBe(37)
})

test('an injected interval drives the poll', async () => {
  let calls = 0
  server.use(
    http.get('/v1/jobs', () => {
      calls++
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  renderHook(() => useScheduleRuns('s1', 20), { wrapper: makeWrapper(newClient()) })
  await waitFor(() => expect(calls).toBeGreaterThanOrEqual(2))
})

test('polls on the DEFAULT 10s interval, and not before', async () => {
  // Behavioral, not constant-reading: useScheduleRuns.test.tsx:42 above only proves
  // SCHEDULE_RUNS_LIMIT is 20 - it says nothing about what refetchInterval the hook
  // actually uses, and a hardcoded 3000 copy-pasted from useJobs.ts:7 would still
  // pass every other test in this file (the second test above only exercises the
  // INJECTED interval seam). Mirrors useSchedule.test.tsx:61-90's positive-control
  // shape: a call-counter proves the instrument is alive, not just that time passed.
  vi.useFakeTimers({ shouldAdvanceTime: true })
  let calls = 0
  server.use(
    http.get('/v1/jobs', () => {
      calls++
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  const { result } = renderHook(() => useScheduleRuns('s1'), { wrapper: makeWrapper(newClient()) })
  await waitFor(() => expect(result.current.status).toBe('success'))
  expect(calls).toBe(1)

  // 9 seconds: still one call. This is the half that fails if the default were 3000.
  await act(async () => {
    vi.advanceTimersByTime(9_000)
  })
  expect(calls).toBe(1)

  // Past 10s: it fires. Positive control on the SAME counter, so the equality
  // above is about the interval and not about a dead instrument.
  await act(async () => {
    vi.advanceTimersByTime(2_000)
  })
  await waitFor(() => expect(calls).toBeGreaterThanOrEqual(2))
})
