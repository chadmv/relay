import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter } from 'react-router-dom'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { renderWithQuery } from '../test/renderWithQuery'
import { SchedulesPage } from './SchedulesPage'

// Hand-written. Every key the server sends. last_job_id and last_job_status appear
// together or not at all, which is the server's pairing contract.
const PAGE = {
  items: [
    {
      id: 's1', name: 'nightly-build', owner_id: 'o1', owner_email: 'dev@studio.com',
      cron_expr: '0 2 * * *', timezone: 'UTC', job_spec: {}, overlap_policy: 'skip',
      enabled: true, next_run_at: '2099-01-01T00:00:00Z', last_run_at: '2026-06-05T11:00:00Z',
      created_at: '2026-06-01T00:00:00Z', updated_at: '2026-06-05T11:00:00Z',
    },
    {
      id: 's2', name: 'weekly-clean', owner_id: 'o1', owner_email: 'dev@studio.com',
      cron_expr: '0 0 * * 0', timezone: 'UTC', job_spec: {}, overlap_policy: 'allow',
      enabled: false, next_run_at: '2099-01-02T00:00:00Z',
      created_at: '2026-06-01T00:00:00Z', updated_at: '2026-06-05T10:00:00Z',
    },
  ],
  next_cursor: '',
  total: 2,
}

// EVERY STATS NUMBER DIFFERS FROM ITS PAGE-DERIVED COUNTERPART. The loaded page
// holds two rows, one enabled and one paused, with total 2; a strip computed from
// data.items would read 1, 1 and 2, and none of those appears below. Without that
// separation a page-derived implementation passes this test.
const STATS = { enabled: 12, paused: 5, total: 17, failed_runs_24h: 3, failing: 2 }

test('the strip shows fleet-wide counts, not page counts', async () => {
  server.use(
    http.get('/v1/scheduled-jobs', () => HttpResponse.json(PAGE)),
    http.get('/v1/scheduled-jobs/stats', () => HttpResponse.json(STATS)),
  )
  renderWithQuery(
    <MemoryRouter>
      <SchedulesPage />
    </MemoryRouter>,
  )
  await screen.findByText('nightly-build')
  // getByText matches only a node's DIRECT text-node children, not text nested
  // inside a child element - the number lives inside a <b>, so the assertion goes
  // through the testid and toHaveTextContent (which reads the full textContent)
  // instead of a page-wide text search.
  expect(await screen.findByTestId('schedules-stat-enabled')).toHaveTextContent('12 ENABLED')
  expect(screen.getByTestId('schedules-stat-paused')).toHaveTextContent('5 PAUSED')
  expect(screen.getByTestId('schedules-stat-failed_runs_24h')).toHaveTextContent('3 FAILED RUNS 24H')
  expect(screen.getByTestId('schedules-stat-failing')).toHaveTextContent('2 FAILING SCHEDULES')
  expect(screen.getByTestId('schedules-stat-total')).toHaveTextContent('17 SCHEDULES TOTAL')
})

// A STATS FAILURE MUST NEVER BLANK THE TABLE. The two queries have separate error
// surfaces; routing the stats error into the page's whole-page error branch would
// replace a working list with a Retry card because a decorative census failed.
test('a stats failure does not blank the table', async () => {
  server.use(
    http.get('/v1/scheduled-jobs', () => HttpResponse.json(PAGE)),
    http.get('/v1/scheduled-jobs/stats', () =>
      HttpResponse.json({ error: 'scheduled job stats failed' }, { status: 500 }),
    ),
  )
  renderWithQuery(
    <MemoryRouter>
      <SchedulesPage />
    </MemoryRouter>,
  )
  expect(await screen.findByText('nightly-build')).toBeInTheDocument()
  expect(screen.getByText('weekly-clean')).toBeInTheDocument()
  expect(await screen.findByText('counts unavailable')).toBeInTheDocument()
  expect(screen.queryByText('Retry')).toBeNull()
})

test('the strip shows placeholders until the first stats response lands', async () => {
  let release!: () => void
  const held = new Promise<void>((res) => { release = res })
  server.use(
    http.get('/v1/scheduled-jobs', () => HttpResponse.json(PAGE)),
    http.get('/v1/scheduled-jobs/stats', async () => {
      await held
      return HttpResponse.json(STATS)
    }),
  )
  renderWithQuery(
    <MemoryRouter>
      <SchedulesPage />
    </MemoryRouter>,
  )
  await screen.findByText('nightly-build')
  expect(screen.getByTestId('schedules-stat-enabled')).toHaveTextContent('- ENABLED')

  release()
  // Same reason as above: the number is nested inside a <b>, so a page-wide text
  // search cannot match it - wait on the testid's own textContent instead.
  await waitFor(() => expect(screen.getByTestId('schedules-stat-enabled')).toHaveTextContent('12 ENABLED'))
})

// BOTH MARKS IN ONE RENDER. They are driven by one derived boolean, so a
// half-applied change - one of the two hard-coded - is caught here rather than by
// two tests that could each pass alone.
test('the two totals label themselves when they can disagree', async () => {
  server.use(
    http.get('/v1/scheduled-jobs', () => HttpResponse.json(PAGE)),
    http.get('/v1/scheduled-jobs/stats', () => HttpResponse.json(STATS)),
  )
  renderWithQuery(
    <MemoryRouter>
      <SchedulesPage />
    </MemoryRouter>,
  )
  await screen.findByText('nightly-build')
  expect(screen.getByTestId('schedules-stat-total')).not.toHaveTextContent('UNFILTERED')
  expect(screen.queryByText(/MATCHING/)).toBeNull()

  await userEvent.click(screen.getByRole('button', { name: 'Enabled' }))
  expect(await screen.findByText(/SCHEDULES TOTAL \(UNFILTERED\)/)).toBeInTheDocument()
  expect(await screen.findByText(/of 2 MATCHING/)).toBeInTheDocument()
})
