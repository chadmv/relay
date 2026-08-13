import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, expect, test, vi } from 'vitest'
import { server } from '../test/setup-helpers'
import { ScheduleDetailPage } from './ScheduleDetailPage'
import type { Schedule } from './api'

const ID = 's1'

function sched(over: Partial<Schedule> = {}): Schedule {
  return {
    id: ID,
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
    ...over,
  }
}

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/schedules/${ID}`]}>
        <Routes>
          <Route path="/schedules/:id" element={<ScheduleDetailPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => vi.useRealTimers())

test('a 10s poll landing mid-edit does NOT overwrite the typed cron', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })

  let detailCalls = 0
  server.use(
    http.get(`/v1/scheduled-jobs/${ID}`, () => {
      detailCalls++
      // After the first response the SERVER's value changes - someone else edited it,
      // or the schedule was touched elsewhere. The poll must not push this into the
      // user's half-typed input.
      return HttpResponse.json(sched({ cron_expr: detailCalls === 1 ? '0 2 * * *' : '0 9 * * 1' }))
    }),
    http.get('/v1/jobs', () => HttpResponse.json({ items: [], next_cursor: '', total: 0 })),
  )

  renderPage()
  const cron = await screen.findByLabelText('Cron expression')
  expect(cron).toHaveValue('0 2 * * *')
  const callsAfterLoad = detailCalls

  await user.clear(cron)
  await user.type(cron, '@every 45m')
  expect(cron).toHaveValue('@every 45m')

  // Cross the 10s poll boundary while the form is dirty.
  await act(async () => {
    await vi.advanceTimersByTimeAsync(11_000)
  })
  // The poll MUST have fired, or this test proves nothing: the input could hold the
  // typed value simply because nothing ever arrived.
  await waitFor(() => expect(detailCalls).toBeGreaterThan(callsAfterLoad))

  // The typed value survives.
  expect(cron).toHaveValue('@every 45m')
})

test('POSITIVE CONTROL: a fresh mount DOES pick up the new server value', async () => {
  // Proves the server fixture really changed and that the form is not simply
  // hardcoded, which would make the test above vacuous.
  server.use(
    http.get(`/v1/scheduled-jobs/${ID}`, () => HttpResponse.json(sched({ cron_expr: '0 9 * * 1' }))),
    http.get('/v1/jobs', () => HttpResponse.json({ items: [], next_cursor: '', total: 0 })),
  )
  renderPage()
  expect(await screen.findByLabelText('Cron expression')).toHaveValue('0 9 * * 1')
})

test('Cancel restores the CURRENTLY loaded server value, not the value at mount', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })

  let detailCalls = 0
  server.use(
    http.get(`/v1/scheduled-jobs/${ID}`, () => {
      detailCalls++
      return HttpResponse.json(sched({ cron_expr: detailCalls === 1 ? '0 2 * * *' : '0 9 * * 1' }))
    }),
    http.get('/v1/jobs', () => HttpResponse.json({ items: [], next_cursor: '', total: 0 })),
  )

  renderPage()
  const cron = await screen.findByLabelText('Cron expression')
  await user.clear(cron)
  await user.type(cron, '@every 45m')

  await act(async () => {
    await vi.advanceTimersByTimeAsync(11_000)
  })
  await waitFor(() => expect(detailCalls).toBeGreaterThan(1))

  await user.click(screen.getByRole('button', { name: 'Cancel' }))
  // Discarding an edit must land on what the server says NOW. Restoring the
  // mount-time value would silently reintroduce a stale cron on the next save - and
  // because that save would carry cron_expr, it would also drift next_run_at.
  expect(cron).toHaveValue('0 9 * * 1')
})
