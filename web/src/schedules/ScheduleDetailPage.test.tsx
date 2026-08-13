import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { expect, test } from 'vitest'
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
    job_spec: { name: 'nightly-build', tasks: [{ name: 'render', command: 'echo hi' }] },
    overlap_policy: 'skip',
    enabled: true,
    next_run_at: '2099-01-01T00:00:00Z',
    created_at: '2026-06-01T00:00:00Z',
    updated_at: '2026-06-05T11:00:00Z',
    ...over,
  }
}

const EMPTY_RUNS = { items: [], next_cursor: '', total: 0 }

function LocationProbe() {
  return <span data-testid="location">{useLocation().pathname}</span>
}

// The page does not use useAuth (the endpoints are owner-or-admin server-side and the
// SPA adds no gate of its own), so no AuthProvider and no /v1/users/me handler.
function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return {
    client,
    ...render(
      <QueryClientProvider client={client}>
        <MemoryRouter initialEntries={[`/schedules/${ID}`]}>
          <LocationProbe />
          <Routes>
            <Route path="/schedules/:id" element={<ScheduleDetailPage />} />
            <Route path="/schedules" element={<span>SCHEDULES LIST</span>} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    ),
  }
}

function handlers(row: Schedule, runs = EMPTY_RUNS) {
  return [
    http.get(`/v1/scheduled-jobs/${ID}`, () => HttpResponse.json(row)),
    http.get('/v1/jobs', () => HttpResponse.json(runs)),
  ]
}

test('renders the breadcrumb, name, ENABLED pill and the three header actions', async () => {
  server.use(...handlers(sched()))
  renderPage()
  expect(await screen.findByText('nightly-build')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: /Schedules/ })).toHaveAttribute('href', '/schedules')
  expect(screen.getByText('ENABLED')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Run now' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Disable' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Delete' })).toBeInTheDocument()
})

test('a paused schedule reads PAUSED, offers Enable, and the next-fire panel is dimmed', async () => {
  server.use(...handlers(sched({ enabled: false })))
  renderPage()
  expect(await screen.findByText('PAUSED')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Enable' })).toBeInTheDocument()
  expect(screen.getByText('paused - no fires queued')).toBeInTheDocument()
  expect(screen.queryByTestId('next-fire-abs')).toBeNull()
})

test('the owner line is ABSENT while owner_email is empty', async () => {
  server.use(...handlers(sched({ owner_email: '' })))
  renderPage()
  await screen.findByText('nightly-build')
  // GET /v1/scheduled-jobs/{id} never calls fillOwnerEmails, so owner_email is
  // always "" today (internal/api/scheduled_jobs.go:508-519). The page must omit the
  // owner entirely rather than render an empty label - and must NOT fall back to
  // owner_id, which is 36 opaque characters.
  expect(screen.queryByText(/owner/i)).toBeNull()
  expect(screen.queryByText('o1')).toBeNull()
})

test('the owner line APPEARS when owner_email is populated (positive control)', async () => {
  // Without this, the test above passes against a page that simply forgot the field.
  // It also pins the behaviour for when the filed enabler lands.
  server.use(...handlers(sched({ owner_email: 'dev@studio.com' })))
  renderPage()
  await screen.findByText('nightly-build')
  expect(screen.getByText('dev@studio.com')).toBeInTheDocument()
})

test('the identity line omits last run and last job when the keys are absent', async () => {
  server.use(...handlers(sched()))
  renderPage()
  await screen.findByText('nightly-build')
  expect(screen.queryByText(/last job/)).toBeNull()
})

test('the last job renders as a link to the job when last_job_id is present', async () => {
  const jobId = 'abcdef12-3456-7890-abcd-ef1234567890'
  server.use(...handlers(sched({ last_job_id: jobId, last_run_at: '2026-06-05T11:00:00Z' })))
  renderPage()
  await screen.findByText('nightly-build')
  expect(screen.getByRole('link', { name: 'abcdef12' })).toHaveAttribute('href', `/jobs/${jobId}`)
})

test('the job spec renders as read-only pretty-printed JSON with no editor', async () => {
  server.use(...handlers(sched()))
  renderPage()
  await screen.findByText('nightly-build')
  // Two-space indented JSON.stringify output, not YAML and not a single line.
  expect(screen.getByText(/"tasks": \[/)).toBeInTheDocument()
  expect(screen.queryByRole('textbox', { name: /spec/i })).toBeNull()
  expect(screen.queryByRole('button', { name: /edit spec/i })).toBeNull()
})

test('the next-fire panel shows exactly one entry', async () => {
  server.use(...handlers(sched()))
  renderPage()
  await screen.findByText('nightly-build')
  expect(screen.getAllByTestId('next-fire-abs')).toHaveLength(1)
  // The hi-fi previews five; a multi-entry preview needs a cron parser web/ does not
  // have. One honest server-supplied value instead.
  expect(screen.getAllByTestId('next-fire-rel')).toHaveLength(1)
})

test('a 404 renders the not-found card with a back link and NO edit or action controls', async () => {
  server.use(
    http.get(`/v1/scheduled-jobs/${ID}`, () =>
      HttpResponse.json({ error: 'scheduled job not found' }, { status: 404 }),
    ),
    http.get('/v1/jobs', () => HttpResponse.json({ error: 'scheduled job not found' }, { status: 404 })),
  )
  renderPage()
  expect(await screen.findByText('Schedule not found.')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: /Schedules/ })).toHaveAttribute('href', '/schedules')
  // ownedScheduledJob 404s a non-owner non-admin identically, so this IS the access
  // denied surface. No Retry (it is not transient), no action bar, no form.
  expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull()
  expect(screen.queryByRole('button', { name: 'Run now' })).toBeNull()
  expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull()
  expect(screen.queryByLabelText('Cron expression')).toBeNull()
})

test('a 500 renders the retryable card and Retry issues exactly one more request', async () => {
  let calls = 0
  server.use(
    http.get(`/v1/scheduled-jobs/${ID}`, () => {
      calls++
      return HttpResponse.json({ error: 'boom' }, { status: 500 })
    }),
    http.get('/v1/jobs', () => HttpResponse.json(EMPTY_RUNS)),
  )
  renderPage()
  const retry = await screen.findByRole('button', { name: 'Retry' })
  const before = calls
  await userEvent.click(retry)
  await waitFor(() => expect(calls).toBe(before + 1))
})

test('Run now POSTs run-now', async () => {
  let posted = false
  server.use(
    ...handlers(sched()),
    http.post(`/v1/scheduled-jobs/${ID}/run-now`, () => {
      posted = true
      return HttpResponse.json({ id: 'job9' }, { status: 201 })
    }),
  )
  renderPage()
  await screen.findByText('nightly-build')
  await userEvent.click(screen.getByRole('button', { name: 'Run now' }))
  await waitFor(() => expect(posted).toBe(true))
})

test('Disable PATCHes exactly { enabled: false }', async () => {
  let body: unknown
  server.use(
    ...handlers(sched()),
    http.patch(`/v1/scheduled-jobs/${ID}`, async ({ request }) => {
      body = await request.json()
      return HttpResponse.json(sched({ enabled: false }))
    }),
  )
  renderPage()
  await screen.findByText('nightly-build')
  await userEvent.click(screen.getByRole('button', { name: 'Disable' }))
  await waitFor(() => expect(body).toEqual({ enabled: false }))
})

test('Delete opens a confirm whose copy states the verified consequences, and Cancel issues NO request', async () => {
  let deletes = 0
  server.use(
    ...handlers(sched()),
    http.delete(`/v1/scheduled-jobs/${ID}`, () => {
      deletes++
      return new HttpResponse(null, { status: 204 })
    }),
  )
  renderPage()
  await screen.findByText('nightly-build')
  await userEvent.click(screen.getByRole('button', { name: 'Delete' }))

  const dialog = await screen.findByRole('dialog')
  // jobs.scheduled_job_id is ON DELETE SET NULL
  // (internal/store/migrations/000006_scheduled_jobs.up.sql:20-21).
  expect(dialog).toHaveTextContent(/unlinked/i)
  expect(dialog).toHaveTextContent(/not cancelled/i)

  await userEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  expect(deletes).toBe(0)
})

test('confirming Delete issues exactly one DELETE and navigates to /schedules', async () => {
  // Positive control on the same counter as the test above.
  let deletes = 0
  server.use(
    ...handlers(sched()),
    http.delete(`/v1/scheduled-jobs/${ID}`, () => {
      deletes++
      return new HttpResponse(null, { status: 204 })
    }),
  )
  renderPage()
  await screen.findByText('nightly-build')
  await userEvent.click(screen.getByRole('button', { name: 'Delete' }))
  const dialog = await screen.findByRole('dialog')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Delete' }))

  await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent('/schedules'))
  expect(deletes).toBe(1)
})

test('a failed save renders the server message inside the form, not in a page banner', async () => {
  server.use(
    ...handlers(sched()),
    http.patch(`/v1/scheduled-jobs/${ID}`, () =>
      HttpResponse.json(
        { error: 'schedule fires faster than minimum interval 30s (observed 1s)' },
        { status: 400 },
      ),
    ),
  )
  renderPage()
  await screen.findByText('nightly-build')
  const cron = screen.getByLabelText('Cron expression')
  await userEvent.clear(cron)
  await userEvent.type(cron, '@every 1s')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))

  const alert = await screen.findByRole('alert')
  expect(alert).toHaveTextContent('minimum interval 30s')
  // The message must sit beside the control that produced it. The form element is
  // the nearest ancestor form; assert containment rather than mere presence.
  expect(cron.closest('form')).toContainElement(alert)
})
