import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { afterEach, expect, test, vi } from 'vitest'
import { server } from '../test/setup-helpers'
import { useSchedule } from './useSchedule'
import type { Schedule } from './api'

const ROW = {
  id: 's1',
  name: 'nightly-build',
  owner_id: 'o1',
  owner_email: '',
  cron_expr: '0 2 * * *',
  timezone: 'UTC',
  job_spec: {},
  overlap_policy: 'skip',
  enabled: true,
  next_run_at: '2099-01-01T00:00:00Z',
  created_at: '2026-06-01T00:00:00Z',
  updated_at: '2026-06-05T11:00:00Z',
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

test('requests the id path and caches under ["schedules","detail",id]', async () => {
  let path: string | undefined
  server.use(
    http.get('/v1/scheduled-jobs/s1', ({ request }) => {
      path = new URL(request.url).pathname
      return HttpResponse.json(ROW)
    }),
  )
  const client = newClient()
  const { result } = renderHook(() => useSchedule('s1'), { wrapper: makeWrapper(client) })
  await waitFor(() => expect(result.current.status).toBe('success'))

  expect(path).toBe('/v1/scheduled-jobs/s1')
  // Nested under the BARE ['schedules'] prefix so useScheduleActions' existing
  // invalidations reach it with NO change to that shared hook's two shipped
  // mutations. 'detail' is not a ScheduleSort (api.ts:28-36), so it cannot collide
  // with useSchedules' ['schedules', sort, cursor] key (useSchedules.ts:10).
  const cached = client.getQueryData<Schedule>(['schedules', 'detail', 's1'])
  expect(cached?.name).toBe('nightly-build')
  // The colliding key must be EMPTY, or the assertion above proves nothing about
  // which key was written.
  expect(client.getQueryData(['schedules', 's1'])).toBeUndefined()
})

test('polls on the DEFAULT 10s interval, and not before', async () => {
  // Behavioral, not constant-reading: this proves the literal at the call site is
  // wired to refetchInterval. A test that imported a constant and compared it to
  // 10000 would stay green if the hook hardcoded 3000.
  vi.useFakeTimers({ shouldAdvanceTime: true })
  let calls = 0
  server.use(
    http.get('/v1/scheduled-jobs/s1', () => {
      calls++
      return HttpResponse.json(ROW)
    }),
  )
  const { result } = renderHook(() => useSchedule('s1'), { wrapper: makeWrapper(newClient()) })
  await waitFor(() => expect(result.current.status).toBe('success'))
  expect(calls).toBe(1)

  // 9 seconds: still one call. This is the half that fails if someone copies
  // useJobs' 3000 (useJobs.ts:7).
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

test('an injected interval overrides the default (test seam, same shape as useSchedules)', async () => {
  let calls = 0
  server.use(
    http.get('/v1/scheduled-jobs/s1', () => {
      calls++
      return HttpResponse.json(ROW)
    }),
  )
  renderHook(() => useSchedule('s1', 20), { wrapper: makeWrapper(newClient()) })
  await waitFor(() => expect(calls).toBeGreaterThanOrEqual(2))
})
