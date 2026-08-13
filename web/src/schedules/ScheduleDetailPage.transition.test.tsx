import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { Link, MemoryRouter, Route, Routes } from 'react-router-dom'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { ScheduleDetailPage } from './ScheduleDetailPage'
import type { Schedule } from './api'

const EMPTY_RUNS = { items: [], next_cursor: '', total: 0 }

function sched(id: string, over: Partial<Schedule> = {}): Schedule {
  return {
    id,
    name: `schedule-${id}`,
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

// A single Route whose element contains BOTH a Link to a sibling id and the page
// itself, so navigating between ids re-renders ScheduleDetailPage with new route
// params WITHOUT unmounting it - the exact detail-to-detail transition the page
// does not otherwise get today, reproduced directly against the component instead
// of waiting for a real link to ship.
function renderWithSiblingLink() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/schedules/s1']}>
        <Routes>
          <Route
            path="/schedules/:id"
            element={
              <>
                <Link to="/schedules/s2">go to s2</Link>
                <ScheduleDetailPage />
              </>
            }
          />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return client
}

test('a detail-to-detail route transition hides the action bar, form and delete control until the new id loads', async () => {
  server.use(
    http.get('/v1/scheduled-jobs/s1', () => HttpResponse.json(sched('s1'))),
    // Never resolves: freezes the page at the id-mismatch moment (route id is s2,
    // cached schedule is still s1 via keepPreviousData) so the assertions below are
    // not racing a fast real response.
    http.get('/v1/scheduled-jobs/s2', () => new Promise(() => {})),
    http.get('/v1/jobs', () => HttpResponse.json(EMPTY_RUNS)),
  )
  renderWithSiblingLink()

  await screen.findByText('schedule-s1')
  expect(screen.getByRole('button', { name: 'Delete' })).toBeInTheDocument()
  expect(screen.getByLabelText('Cron expression')).toBeInTheDocument()

  await userEvent.click(screen.getByRole('link', { name: 'go to s2' }))

  // The route id is now s2 while the loaded schedule is still s1's row: none of the
  // id-bearing controls may render against that mismatch.
  await waitFor(() => expect(screen.queryByText('schedule-s1')).toBeNull())
  expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull()
  expect(screen.queryByRole('button', { name: 'Run now' })).toBeNull()
  expect(screen.queryByLabelText('Cron expression')).toBeNull()
})

test('once the new id loads, the trigger form seeds from the NEW schedule, not a stale draft', async () => {
  server.use(
    http.get('/v1/scheduled-jobs/s1', () => HttpResponse.json(sched('s1', { cron_expr: '0 2 * * *' }))),
    http.get('/v1/scheduled-jobs/s2', () => HttpResponse.json(sched('s2', { cron_expr: '0 9 * * 1' }))),
    http.get('/v1/jobs', () => HttpResponse.json(EMPTY_RUNS)),
  )
  renderWithSiblingLink()

  const cron = await screen.findByLabelText('Cron expression')
  expect(cron).toHaveValue('0 2 * * *')

  await userEvent.click(screen.getByRole('link', { name: 'go to s2' }))

  await screen.findByText('schedule-s2')
  // A stale draft would still read s1's cron here (ScheduleTriggerForm seeds its
  // useState exactly once). It must read s2's value instead.
  expect(screen.getByLabelText('Cron expression')).toHaveValue('0 9 * * 1')
})

test('the delete confirm dialog cannot name a schedule other than the one remove.mutate would target', async () => {
  server.use(
    http.get('/v1/scheduled-jobs/s1', () => HttpResponse.json(sched('s1'))),
    http.get('/v1/scheduled-jobs/s2', () => new Promise(() => {})),
    http.get('/v1/jobs', () => HttpResponse.json(EMPTY_RUNS)),
  )
  renderWithSiblingLink()

  await screen.findByText('schedule-s1')
  await userEvent.click(screen.getByRole('button', { name: 'Delete' }))
  expect(await screen.findByRole('dialog')).toHaveTextContent('schedule-s1')

  // Navigate to s2 while its fetch never resolves. The background link is behind
  // the dialog's own aria-hidden marking (dialogStack.ts), so it must be queried
  // with `hidden: true` to be found at all - that marking is exactly why a real
  // user cannot click through to it either, but the route can still change under
  // the dialog via other means (browser back/forward), which this simulates. If
  // the dialog were still driven by the stale s1 schedule object, it would keep
  // naming "schedule-s1" while the route (and therefore what remove.mutate(id)
  // would delete) is s2.
  await userEvent.click(screen.getByRole('link', { name: 'go to s2', hidden: true }))
  await waitFor(() => expect(screen.queryByText('schedule-s1')).toBeNull())
  expect(screen.queryByRole('dialog')).toBeNull()
})
