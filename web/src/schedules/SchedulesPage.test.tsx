import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter } from 'react-router-dom'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { renderWithQuery } from '../test/renderWithQuery'
import { SchedulesPage } from './SchedulesPage'

const page = {
  items: [
    {
      id: 's1', name: 'nightly-build', owner_id: 'o1', owner_email: 'dev@studio.com',
      cron_expr: '0 2 * * *', timezone: 'UTC', job_spec: {}, overlap_policy: 'skip',
      enabled: true, next_run_at: '2099-01-01T00:00:00Z', last_run_at: '2026-06-05T11:00:00Z',
      last_job_id: 'abcdef12-3456', created_at: '2026-06-01T00:00:00Z', updated_at: '2026-06-05T11:00:00Z',
    },
    {
      id: 's2', name: 'weekly-clean', owner_id: 'o1', owner_email: 'dev@studio.com',
      cron_expr: '0 0 * * 0', timezone: 'UTC', job_spec: {}, overlap_policy: 'allow',
      enabled: false, next_run_at: '2099-01-02T00:00:00Z', created_at: '2026-06-01T00:00:00Z',
      updated_at: '2026-06-05T10:00:00Z',
    },
  ],
  next_cursor: '',
  total: 2,
}

test('renders schedules and the page-scoped summary', async () => {
  server.use(http.get('/v1/scheduled-jobs', () => HttpResponse.json(page)))
  renderWithQuery(
    <MemoryRouter>
      <SchedulesPage />
    </MemoryRouter>,
  )
  expect(await screen.findByText('nightly-build')).toBeInTheDocument()
  expect(screen.getByText('weekly-clean')).toBeInTheDocument()
  expect(screen.getByText('2 schedules')).toBeInTheDocument()
})

test('shows the empty state when there are no schedules', async () => {
  server.use(http.get('/v1/scheduled-jobs', () => HttpResponse.json({ items: [], next_cursor: '', total: 0 })))
  renderWithQuery(
    <MemoryRouter>
      <SchedulesPage />
    </MemoryRouter>,
  )
  expect(await screen.findByText('No schedules yet.')).toBeInTheDocument()
})

test('shows the error state with a Retry button', async () => {
  server.use(http.get('/v1/scheduled-jobs', () => HttpResponse.json({ error: 'boom' }, { status: 500 })))
  renderWithQuery(
    <MemoryRouter>
      <SchedulesPage />
    </MemoryRouter>,
  )
  expect(await screen.findByText('Retry')).toBeInTheDocument()
})

test('changing the sort re-requests with the new sort key', async () => {
  const sorts: (string | null)[] = []
  server.use(
    http.get('/v1/scheduled-jobs', ({ request }) => {
      sorts.push(new URL(request.url).searchParams.get('sort'))
      return HttpResponse.json(page)
    }),
  )
  renderWithQuery(
    <MemoryRouter>
      <SchedulesPage />
    </MemoryRouter>,
  )
  await screen.findByText('nightly-build')
  await userEvent.selectOptions(screen.getByLabelText('Sort'), 'name')
  await waitFor(() => expect(sorts).toContain('name'))
})

test('next/prev pagination walks the cursor', async () => {
  const cursors: (string | null)[] = []
  server.use(
    http.get('/v1/scheduled-jobs', ({ request }) => {
      const c = new URL(request.url).searchParams.get('cursor')
      cursors.push(c)
      // First page returns a next_cursor; second page returns none.
      return HttpResponse.json({ ...page, next_cursor: c ? '' : 'CUR2' })
    }),
  )
  renderWithQuery(
    <MemoryRouter>
      <SchedulesPage />
    </MemoryRouter>,
  )
  await screen.findByText('nightly-build')
  await userEvent.click(screen.getByRole('button', { name: /next/i }))
  await waitFor(() => expect(cursors).toContain('CUR2'))
  await userEvent.click(screen.getByRole('button', { name: /prev/i }))
  await waitFor(() => expect(cursors.filter((c) => c === null).length).toBeGreaterThanOrEqual(2))
})

test('next and prev are disabled while a page fetch is in flight', async () => {
  let resolvePage2!: () => void
  const page2Ready = new Promise<void>((res) => { resolvePage2 = res })

  const makeSchedule = (id: string, name: string) => ({
    id, name, owner_id: 'o1', owner_email: 'dev@studio.com',
    cron_expr: '0 2 * * *', timezone: 'UTC', job_spec: {}, overlap_policy: 'skip',
    enabled: true, next_run_at: '2099-01-01T00:00:00Z', last_run_at: '2026-06-05T11:00:00Z',
    last_job_id: null, created_at: '2026-06-01T00:00:00Z', updated_at: '2026-06-05T11:00:00Z',
  })

  const pages: Record<string, { items: unknown[]; next_cursor: string; total: number }> = {
    '': {
      items: [makeSchedule('s-A', 'sched-A')],
      next_cursor: 'CUR1', total: 2,
    },
    CUR1: {
      items: [makeSchedule('s-B', 'sched-B')],
      next_cursor: '', total: 2,
    },
  }
  server.use(
    http.get('/v1/scheduled-jobs', async ({ request }) => {
      const cur = new URL(request.url).searchParams.get('cursor') ?? ''
      if (cur === 'CUR1') await page2Ready
      return HttpResponse.json(pages[cur])
    }),
  )
  renderWithQuery(
    <MemoryRouter>
      <SchedulesPage />
    </MemoryRouter>,
  )

  // Page 1 loaded: prev disabled, next enabled.
  expect(await screen.findByText('sched-A')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /prev/i })).toBeDisabled()
  expect(screen.getByRole('button', { name: /next/i })).toBeEnabled()

  // Click next; fetch is in flight (hanging). Both buttons must be disabled.
  await userEvent.click(screen.getByRole('button', { name: /next/i }))
  await waitFor(() => {
    expect(screen.getByRole('button', { name: /next/i })).toBeDisabled()
    expect(screen.getByRole('button', { name: /prev/i })).toBeDisabled()
  })

  // Resolve the fetch; page 2 lands.
  resolvePage2()
  expect(await screen.findByText('sched-B')).toBeInTheDocument()
  // Page 2: no next_cursor so next disabled; prev should be re-enabled.
  await waitFor(() => expect(screen.getByRole('button', { name: /next/i })).toBeDisabled())
  expect(screen.getByRole('button', { name: /prev/i })).toBeEnabled()

  // Go back to page 1.
  await userEvent.click(screen.getByRole('button', { name: /prev/i }))
  expect(await screen.findByText('sched-A')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /prev/i })).toBeDisabled()
})

// Helper to build a page of schedule items.
function makeSchedules(count: number, startId = 0) {
  return Array.from({ length: count }, (_, i) => ({
    id: `s${startId + i}`,
    name: `sched-${startId + i}`,
    owner_id: 'o1',
    owner_email: 'dev@studio.com',
    cron_expr: '0 2 * * *',
    timezone: 'UTC',
    job_spec: {},
    overlap_policy: 'skip',
    enabled: true,
    next_run_at: '2099-01-01T00:00:00Z',
    last_run_at: '2026-06-05T11:00:00Z',
    last_job_id: null,
    created_at: '2026-06-01T00:00:00Z',
    updated_at: '2026-06-05T11:00:00Z',
  }))
}

test('pagination footer shows 1-N of total on the first full page', async () => {
  server.use(
    http.get('/v1/scheduled-jobs', () =>
      HttpResponse.json({ items: makeSchedules(50), next_cursor: 'CUR1', total: 120 }),
    ),
  )
  renderWithQuery(
    <MemoryRouter>
      <SchedulesPage />
    </MemoryRouter>,
  )
  await screen.findByText('sched-0')
  expect(await screen.findByText(/1-50 of 120/i)).toBeInTheDocument()
})

test('pagination footer shows correct absolute range on partial last page after paging forward', async () => {
  const pages: Record<string, { items: unknown[]; next_cursor: string; total: number }> = {
    '': { items: makeSchedules(50, 0), next_cursor: 'CUR1', total: 63 },
    CUR1: { items: makeSchedules(13, 50), next_cursor: '', total: 63 },
  }
  server.use(
    http.get('/v1/scheduled-jobs', ({ request }) => {
      const cur = new URL(request.url).searchParams.get('cursor') ?? ''
      return HttpResponse.json(pages[cur])
    }),
  )
  renderWithQuery(
    <MemoryRouter>
      <SchedulesPage />
    </MemoryRouter>,
  )
  await screen.findByText('sched-0')
  expect(await screen.findByText(/1-50 of 63/i)).toBeInTheDocument()

  await userEvent.click(screen.getByRole('button', { name: /next/i }))
  await screen.findByText('sched-50')
  expect(await screen.findByText(/51-63 of 63/i)).toBeInTheDocument()
})

test('pagination footer restores prior range when paging back', async () => {
  const pages: Record<string, { items: unknown[]; next_cursor: string; total: number }> = {
    '': { items: makeSchedules(50, 0), next_cursor: 'CUR1', total: 63 },
    CUR1: { items: makeSchedules(13, 50), next_cursor: '', total: 63 },
  }
  server.use(
    http.get('/v1/scheduled-jobs', ({ request }) => {
      const cur = new URL(request.url).searchParams.get('cursor') ?? ''
      return HttpResponse.json(pages[cur])
    }),
  )
  renderWithQuery(
    <MemoryRouter>
      <SchedulesPage />
    </MemoryRouter>,
  )
  await screen.findByText('sched-0')
  expect(await screen.findByText(/1-50 of 63/i)).toBeInTheDocument()

  await userEvent.click(screen.getByRole('button', { name: /next/i }))
  await screen.findByText('sched-50')
  expect(await screen.findByText(/51-63 of 63/i)).toBeInTheDocument()

  await userEvent.click(screen.getByRole('button', { name: /prev/i }))
  await screen.findByText('sched-0')
  expect(await screen.findByText(/1-50 of 63/i)).toBeInTheDocument()
})

test('the footer thousands-separates a four-digit total', async () => {
  server.use(
    http.get('/v1/scheduled-jobs', () =>
      HttpResponse.json({ items: makeSchedules(50), next_cursor: '', total: 2341 }),
    ),
  )
  renderWithQuery(
    <MemoryRouter>
      <SchedulesPage />
    </MemoryRouter>,
  )
  await screen.findByText('sched-0')
  expect(await screen.findByText('1-50 of 2,341')).toBeInTheDocument()
})

// A literal, never (2341).toLocaleString(): comparing the component's output against
// the same call it makes would pass on any runner, including one with no group
// separator, and assert nothing.
//
// The total is 1234 rather than 0 so the assertion discriminates on both axes at once.
// A range with no zero-rows branch reads `0-0 of 1,234` and a zero-rows branch that
// does not format reads `0 of 1234`, both distinct from `0 of 1,234`, so a
// half-applied change cannot pass. A total of 0 would leave
// the formatting half unpinned.
test('the footer renders a bare count and no range when the page has no rows', async () => {
  server.use(
    http.get('/v1/scheduled-jobs', () =>
      HttpResponse.json({ items: [], next_cursor: '', total: 1234 }),
    ),
  )
  renderWithQuery(
    <MemoryRouter>
      <SchedulesPage />
    </MemoryRouter>,
  )
  await screen.findByText('No schedules yet.')
  expect(await screen.findByText('0 of 1,234')).toBeInTheDocument()
})

test('clicking Disable PATCHes the schedule', async () => {
  let patched: unknown
  server.use(
    http.get('/v1/scheduled-jobs', () => HttpResponse.json(page)),
    http.patch('/v1/scheduled-jobs/s1', async ({ request }) => {
      patched = await request.json()
      return HttpResponse.json({ ...page.items[0], enabled: false })
    }),
  )
  renderWithQuery(
    <MemoryRouter>
      <SchedulesPage />
    </MemoryRouter>,
  )
  await screen.findByText('nightly-build')
  await userEvent.click(screen.getAllByRole('button', { name: 'Disable' })[0])
  await waitFor(() => expect(patched).toEqual({ enabled: false }))
})
