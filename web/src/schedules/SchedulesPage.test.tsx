import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
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
  renderWithQuery(<SchedulesPage />)
  expect(await screen.findByText('nightly-build')).toBeInTheDocument()
  expect(screen.getByText('weekly-clean')).toBeInTheDocument()
  expect(screen.getByText('2 schedules')).toBeInTheDocument()
})

test('shows the empty state when there are no schedules', async () => {
  server.use(http.get('/v1/scheduled-jobs', () => HttpResponse.json({ items: [], next_cursor: '', total: 0 })))
  renderWithQuery(<SchedulesPage />)
  expect(await screen.findByText('No schedules yet.')).toBeInTheDocument()
})

test('shows the error state with a Retry button', async () => {
  server.use(http.get('/v1/scheduled-jobs', () => HttpResponse.json({ error: 'boom' }, { status: 500 })))
  renderWithQuery(<SchedulesPage />)
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
  renderWithQuery(<SchedulesPage />)
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
  renderWithQuery(<SchedulesPage />)
  await screen.findByText('nightly-build')
  await userEvent.click(screen.getByRole('button', { name: /next/i }))
  await waitFor(() => expect(cursors).toContain('CUR2'))
  await userEvent.click(screen.getByRole('button', { name: /prev/i }))
  await waitFor(() => expect(cursors.filter((c) => c === null).length).toBeGreaterThanOrEqual(2))
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
  renderWithQuery(<SchedulesPage />)
  await screen.findByText('nightly-build')
  await userEvent.click(screen.getAllByRole('button', { name: 'Disable' })[0])
  await waitFor(() => expect(patched).toEqual({ enabled: false }))
})
