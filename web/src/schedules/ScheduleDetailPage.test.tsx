import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { ScheduleDetailPage } from './ScheduleDetailPage'
import type { Schedule } from './api'
import { formatStarted } from '../jobs/status'

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
  // GET /v1/scheduled-jobs/{id} populates owner_email via fillOwnerEmails, but that
  // helper is best-effort and leaves the field "" when the owner lookup fails. The
  // page must omit the owner entirely rather than render an empty label - and must
  // NOT fall back to owner_id, which is 36 opaque characters.
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

test('a newer settled action is not masked by an older settled action error', async () => {
  let current = sched()
  let enabledCalls = 0
  server.use(
    http.get(`/v1/scheduled-jobs/${ID}`, () => HttpResponse.json(current)),
    http.get('/v1/jobs', () => HttpResponse.json(EMPTY_RUNS)),
    http.post(`/v1/scheduled-jobs/${ID}/run-now`, () =>
      HttpResponse.json({ error: 'run now boom' }, { status: 500 }),
    ),
    http.patch(`/v1/scheduled-jobs/${ID}`, () => {
      enabledCalls++
      // First PATCH (Disable) succeeds; second PATCH (Enable) fails.
      if (enabledCalls === 1) {
        current = { ...current, enabled: false }
        return HttpResponse.json(current)
      }
      return HttpResponse.json({ error: 'enable boom' }, { status: 500 })
    }),
  )
  renderPage()
  await screen.findByText('nightly-build')

  // 1. Run now fails: the banner shows its error.
  await userEvent.click(screen.getByRole('button', { name: 'Run now' }))
  const firstAlert = await screen.findByRole('alert')
  expect(firstAlert).toHaveTextContent('run now boom')

  // 2. Disable succeeds. The stale Run now error must not keep the banner up.
  await userEvent.click(screen.getByRole('button', { name: 'Disable' }))
  await waitFor(() => expect(enabledCalls).toBe(1))
  await waitFor(() => expect(screen.queryByRole('alert')).toBeNull())

  // 3. Enable then fails: its OWN error must be the one shown, not silently
  // swallowed by the ?? chain picking up the old (never-reset) Run now error first.
  await userEvent.click(await screen.findByRole('button', { name: 'Enable' }))
  await waitFor(() => expect(enabledCalls).toBe(2))
  const secondAlert = await screen.findByRole('alert')
  expect(secondAlert).toHaveTextContent('enable boom')
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

test('after a 400 then Cancel, no alert remains in the DOM', async () => {
  server.use(
    ...handlers(sched()),
    http.patch(`/v1/scheduled-jobs/${ID}`, () =>
      HttpResponse.json({ error: 'schedule fires faster than minimum interval 30s' }, { status: 400 }),
    ),
  )
  renderPage()
  await screen.findByText('nightly-build')
  const cron = screen.getByLabelText('Cron expression')
  await userEvent.clear(cron)
  await userEvent.type(cron, '@every 1s')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await screen.findByRole('alert')

  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  // Cancel restores the fields, but the stale server error must not survive it. A
  // subsequent unchanged Save hits the empty-patch early return BEFORE onSubmit
  // (and therefore update.reset()) ever runs, so the banner would otherwise persist
  // until the component unmounts.
  expect(screen.queryByRole('alert')).toBeNull()
})

test('after a 400, Cancel, then an unchanged Save, no alert remains in the DOM either', async () => {
  server.use(
    ...handlers(sched()),
    http.patch(`/v1/scheduled-jobs/${ID}`, () =>
      HttpResponse.json({ error: 'schedule fires faster than minimum interval 30s' }, { status: 400 }),
    ),
  )
  renderPage()
  await screen.findByText('nightly-build')
  const cron = screen.getByLabelText('Cron expression')
  await userEvent.clear(cron)
  await userEvent.type(cron, '@every 1s')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await screen.findByRole('alert')

  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  // The empty-patch early return in ScheduleTriggerForm.submit fires here, never
  // reaching onSubmit / update.reset() - the dismissal must not depend on that path.
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  expect(screen.queryByRole('alert')).toBeNull()
})

test('Save changes is disabled while a header action (Run now) is pending', async () => {
  let resolveRunNow: (() => void) | undefined
  server.use(
    ...handlers(sched()),
    http.post(
      `/v1/scheduled-jobs/${ID}/run-now`,
      () =>
        new Promise((resolve) => {
          resolveRunNow = () => resolve(HttpResponse.json({ id: 'job1' }, { status: 201 }))
        }),
    ),
  )
  renderPage()
  await screen.findByText('nightly-build')
  await userEvent.click(screen.getByRole('button', { name: 'Run now' }))

  await waitFor(() => expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled())
  resolveRunNow?.()
  await waitFor(() => expect(screen.getByRole('button', { name: 'Save changes' })).not.toBeDisabled())
})

test('the header actions are disabled while Save is pending', async () => {
  let resolvePatch: (() => void) | undefined
  server.use(
    ...handlers(sched()),
    http.patch(
      `/v1/scheduled-jobs/${ID}`,
      () =>
        new Promise((resolve) => {
          resolvePatch = () => resolve(HttpResponse.json(sched({ overlap_policy: 'allow' })))
        }),
    ),
  )
  renderPage()
  await screen.findByText('nightly-build')
  await userEvent.click(screen.getByRole('button', { name: 'allow' }))
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))

  await waitFor(() => expect(screen.getByRole('button', { name: 'Run now' })).toBeDisabled())
  expect(screen.getByRole('button', { name: 'Disable' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Delete' })).toBeDisabled()
  resolvePatch?.()
  await waitFor(() => expect(screen.getByRole('button', { name: 'Run now' })).not.toBeDisabled())
})

// The schedule's next fire is soon; a recompute would push it out by a full hour.
const NEXT_SOON = '2099-01-01T00:05:00Z'
const NEXT_DRIFTED = '2099-01-01T01:00:00Z'

// Transcription of internal/api/scheduled_jobs.go:585-596: next_run_at is recomputed
// from time.Now() whenever the request CARRIES a cron_expr or timezone key, changed
// or not. `current` is mutated so the invalidated GET refetch serves the same row the
// PATCH produced, exactly as the real server does.
function driftServer(initial: Schedule) {
  let current = initial
  const bodies: Record<string, unknown>[] = []
  server.use(
    http.get(`/v1/scheduled-jobs/${ID}`, () => HttpResponse.json(current)),
    http.get('/v1/jobs', () => HttpResponse.json(EMPTY_RUNS)),
    http.patch(`/v1/scheduled-jobs/${ID}`, async ({ request }) => {
      const body = (await request.json()) as Record<string, unknown>
      bodies.push(body)
      const recomputes = 'cron_expr' in body || 'timezone' in body
      current = {
        ...current,
        ...(body as Partial<Schedule>),
        next_run_at: recomputes ? NEXT_DRIFTED : current.next_run_at,
      }
      return HttpResponse.json(current)
    }),
  )
  return { bodies }
}

test('the two next-fire fixtures render differently, so the panel assertions below can discriminate', () => {
  // Guards the instrument itself: if formatStarted collapsed these two instants to
  // the same string in the runner's timezone, both drift tests would be vacuous.
  expect(formatStarted(NEXT_SOON)).not.toBe(formatStarted(NEXT_DRIFTED))
})

test('DRIFT REGRESSION: saving only the overlap policy does NOT move the next fire time', async () => {
  const { bodies } = driftServer(sched({ next_run_at: NEXT_SOON }))
  renderPage()
  await screen.findByText('nightly-build')
  expect(screen.getByTestId('next-fire-abs')).toHaveTextContent(formatStarted(NEXT_SOON))

  await userEvent.click(screen.getByRole('button', { name: 'allow' }))
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))

  await waitFor(() => expect(bodies).toHaveLength(1))
  // The body must not carry cron_expr or timezone. This is the cause...
  expect(bodies[0]).toEqual({ overlap_policy: 'allow' })
  // ...and this is the user-visible effect, which is what actually matters: an
  // implementation that posts the whole form pushes the next fire out by 55 minutes
  // and nothing else in the app complains.
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'allow' })).toHaveAttribute('aria-pressed', 'true'),
  )
  expect(screen.getByTestId('next-fire-abs')).toHaveTextContent(formatStarted(NEXT_SOON))
  expect(screen.getByTestId('next-fire-abs')).not.toHaveTextContent(formatStarted(NEXT_DRIFTED))
})

test('POSITIVE CONTROL: saving a changed cron DOES move the next fire time', async () => {
  // Without this, the test above passes against a page whose next-fire panel is
  // static and never reflects a save at all.
  const { bodies } = driftServer(sched({ next_run_at: NEXT_SOON }))
  renderPage()
  await screen.findByText('nightly-build')
  expect(screen.getByTestId('next-fire-abs')).toHaveTextContent(formatStarted(NEXT_SOON))

  const cron = screen.getByLabelText('Cron expression')
  await userEvent.clear(cron)
  await userEvent.type(cron, '@every 1h')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))

  await waitFor(() => expect(bodies).toEqual([{ cron_expr: '@every 1h' }]))
  await waitFor(() =>
    expect(screen.getByTestId('next-fire-abs')).toHaveTextContent(formatStarted(NEXT_DRIFTED)),
  )
})

test('a clean Save issues ZERO requests', async () => {
  const { bodies } = driftServer(sched({ next_run_at: NEXT_SOON }))
  renderPage()
  await screen.findByText('nightly-build')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  // No PATCH at all - not an empty one. An empty PATCH is harmless server-side today,
  // but "no change means no request" is the property being pinned.
  await new Promise((r) => setTimeout(r, 50))
  expect(bodies).toEqual([])
})

test('saving twice in a row issues exactly one request: after a save the draft is clean again', async () => {
  const { bodies } = driftServer(sched({ next_run_at: NEXT_SOON }))
  renderPage()
  await screen.findByText('nightly-build')
  await userEvent.click(screen.getByRole('button', { name: 'allow' }))
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() => expect(bodies).toHaveLength(1))

  // The refetched row now says overlap_policy: 'allow', so the draft matches it and a
  // second Save must be a no-op. An implementation that re-armed the form from the
  // mutation response, or that tracked dirtiness with a flag, would fire again here -
  // and if it re-sent cron_expr, that second request would drift next_run_at.
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await new Promise((r) => setTimeout(r, 50))
  expect(bodies).toHaveLength(1)
})

const FAILURE = 'task render: retries must be between 0 and 10'
const FAILED_AT = '2026-06-05T11:04:00Z'

// THE SUB-LINE MARKER is what makes the failure visible WITHOUT SCROLLING. The
// identity line already reads created / updated / next fire / last run, and a
// dead schedule's tell is that "last run" stopped moving while "next fire" kept
// going - a pair the reader has to interpret. "last failure 4 minutes ago"
// beside "last run 22 days ago" is the sentence an operator understands
// immediately, and it is why last_error_at earns its column.
test('a schedule carrying a failure shows the sub-line marker and the Last failure panel', async () => {
  server.use(...handlers(sched({ last_error: FAILURE, last_error_at: FAILED_AT })))
  renderPage()

  expect(await screen.findByText('nightly-build')).toBeInTheDocument()
  expect(screen.getByTestId('last-failure-rel')).toBeInTheDocument()

  expect(screen.getByText('Last failure')).toBeInTheDocument()
  expect(screen.getByTestId('last-error-text')).toHaveTextContent(FAILURE)
  expect(screen.getByTestId('last-error-when')).toBeInTheDocument()
})

// THE ABSENCE CASE, and the one that keeps a healthy schedule's layout identical
// to what it was before this slice.
test('a healthy schedule renders neither the marker nor the panel', async () => {
  server.use(...handlers(sched()))
  renderPage()

  expect(await screen.findByText('nightly-build')).toBeInTheDocument()
  expect(screen.queryByTestId('last-failure-rel')).toBeNull()
  expect(screen.queryByText('Last failure')).toBeNull()
  expect(screen.queryByTestId('last-error-text')).toBeNull()
})

// ABSENT, EMPTY STRING AND PRESENT ARE THREE DIFFERENT THINGS, and conflating the
// first two is the shape of the defect this whole slice exists to close. The
// server omits last_error entirely for a healthy schedule and never stores "",
// but a read written as `last_error !== undefined` would open the panel on an
// empty string and show an operator a failure heading with no reason under it.
// A truthiness test gets all three right; this pins it so a later rewrite to a
// more "explicit" undefined check goes red here instead of shipping.
test('an empty last_error opens neither the marker nor the panel', async () => {
  server.use(...handlers(sched({ last_error: '', last_error_at: FAILED_AT })))
  renderPage()

  expect(await screen.findByText('nightly-build')).toBeInTheDocument()
  expect(screen.queryByTestId('last-error-text')).toBeNull()
  expect(screen.queryByText('Last failure')).toBeNull()
  // AND the sub-line marker goes with it. The marker's only job is to point at
  // the panel, so it must never render when there is no panel to point at.
  expect(screen.queryByTestId('last-failure-rel')).toBeNull()
})

// THE FAILURE TEXT IS A TEXT CHILD, NEVER MARKUP. It is derived from the stored
// job_spec and embeds a task name the schedule's OWNER chose, and an admin can
// read any user's schedule - so in the admin case it is partly attacker-chosen
// prose. There is no counter here to inflate and nothing an owner gains by
// breaking their own schedule; the one real risk is display-layer
// impersonation, text crafted to read like relay's own chrome. Same rule the Job
// spec panel states, for the same reason.
test('the failure text is escaped, not interpreted as markup', async () => {
  const hostile = '<b data-testid="injected">relay: click here to continue</b>'
  server.use(...handlers(sched({ last_error: hostile, last_error_at: FAILED_AT })))
  renderPage()

  expect(await screen.findByTestId('last-error-text')).toHaveTextContent(hostile)
  expect(screen.queryByTestId('injected')).toBeNull()
})

// THE ENABLED PILL IS UNTOUCHED. The schedule IS enabled; the pill is telling
// the truth and it is the operator's own setting. Failure is a separate axis and
// gets a separate element, so no third state is added to the chip.
test('a failing schedule still reads ENABLED', async () => {
  server.use(...handlers(sched({ last_error: FAILURE, last_error_at: FAILED_AT })))
  renderPage()
  expect(await screen.findByText('ENABLED')).toBeInTheDocument()
  expect(screen.queryByText('PAUSED')).toBeNull()
})

// last_error WITHOUT last_error_at. The two are separate nullable columns, so
// nothing in the database forces them to move together, and a panel that
// unconditionally read last_error_at would crash or render "Invalid Date".
test('a failure with no timestamp renders the panel without the time line', async () => {
  server.use(...handlers(sched({ last_error: FAILURE })))
  renderPage()
  expect(await screen.findByTestId('last-error-text')).toHaveTextContent(FAILURE)
  expect(screen.queryByTestId('last-error-when')).toBeNull()
  expect(screen.queryByTestId('last-failure-rel')).toBeNull()
})

// THE OTHER HALF OF THAT PAIR, which the case above cannot see: last_error_at
// WITHOUT last_error. Two separate nullable columns means both one-sided states
// are reachable, and the sub-line marker is the element at risk here because it
// is the one keyed on the timestamp. A marker reading "last failure 4 minutes
// ago" with no panel anywhere on the page tells an operator a failure happened
// and then refuses to say what it was.
test('a timestamp with no failure text renders no marker and no panel', async () => {
  server.use(...handlers(sched({ last_error_at: FAILED_AT })))
  renderPage()

  expect(await screen.findByText('nightly-build')).toBeInTheDocument()
  expect(screen.queryByTestId('last-failure-rel')).toBeNull()
  expect(screen.queryByText('Last failure')).toBeNull()
})
