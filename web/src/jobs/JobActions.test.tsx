import { render, renderHook, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { server } from '../test/setup-helpers'
import { JobActions } from './JobActions'
import { useJobStats } from './useJobStats'
import type { JobDetail } from './api'

const ID = 'j1'

const JOB: JobDetail = {
  id: ID,
  name: 'shot-042 render',
  priority: 'high',
  status: 'running',
  submitted_by: 'u1',
  labels: null,
  tasks: [],
  created_at: '2026-07-01T00:00:00Z',
  updated_at: '2026-07-01T00:01:00Z',
}

function renderActions(job: JobDetail) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <JobActions job={job} />
    </QueryClientProvider>,
  )
}

test('a running job shows Cancel and Force cancel buttons', () => {
  renderActions(JOB)
  expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Force cancel' })).toBeInTheDocument()
})

test('graceful cancel confirms and DELETEs without ?force=true', async () => {
  let search = ''
  server.use(
    http.delete(`/v1/jobs/${ID}`, ({ request }) => {
      search = new URL(request.url).search
      return HttpResponse.json({ ...JOB, status: 'cancelled' })
    }),
  )
  renderActions(JOB)
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  const dialog = screen.getByRole('dialog')
  // Primary action label is "Cancel job" (not "Cancel") to disambiguate from the
  // dialog's own "Cancel" dismiss button.
  await userEvent.click(within(dialog).getByRole('button', { name: 'Cancel job' }))
  await waitFor(() => expect(search).toBe(''))
})

test('force cancel confirms and DELETEs with ?force=true', async () => {
  let force: string | null = null
  server.use(
    http.delete(`/v1/jobs/${ID}`, ({ request }) => {
      force = new URL(request.url).searchParams.get('force')
      return HttpResponse.json({ ...JOB, status: 'cancelled' })
    }),
  )
  renderActions(JOB)
  await userEvent.click(screen.getByRole('button', { name: 'Force cancel' }))
  const dialog = screen.getByRole('dialog')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Force cancel' }))
  await waitFor(() => expect(force).toBe('true'))
})

test('dismissing the confirm dialog fires no request', async () => {
  let hits = 0
  server.use(
    http.delete(`/v1/jobs/${ID}`, () => {
      hits++
      return HttpResponse.json({ ...JOB, status: 'cancelled' })
    }),
  )
  renderActions(JOB)
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Cancel' }))
  await new Promise((r) => setTimeout(r, 20))
  expect(hits).toBe(0)
})

test('Escape dismisses the dialog and fires no request', async () => {
  let hits = 0
  server.use(
    http.delete(`/v1/jobs/${ID}`, () => {
      hits++
      return HttpResponse.json({ ...JOB, status: 'cancelled' })
    }),
  )
  renderActions(JOB)
  await userEvent.click(screen.getByRole('button', { name: 'Force cancel' }))
  expect(screen.getByRole('dialog')).toBeInTheDocument()
  await userEvent.keyboard('{Escape}')
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  expect(hits).toBe(0)
})

test('a done job hides both buttons', () => {
  renderActions({ ...JOB, status: 'done' })
  expect(screen.queryByRole('button', { name: 'Cancel' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Force cancel' })).not.toBeInTheDocument()
})

test('a cancelled job hides both buttons', () => {
  renderActions({ ...JOB, status: 'cancelled' })
  expect(screen.queryByRole('button', { name: 'Cancel' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Force cancel' })).not.toBeInTheDocument()
})

test('a failed job STILL shows both buttons (server allows cancel of failed)', () => {
  renderActions({ ...JOB, status: 'failed' })
  expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Force cancel' })).toBeInTheDocument()
})

test('the stats query refetches after a successful cancel (three-key invalidation)', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  let statsCalls = 0
  server.use(
    http.get('/v1/jobs/stats', () => {
      statsCalls++
      return HttpResponse.json({ running: 0, queued: 0, done_24h: 0, failed_24h: 0 })
    }),
    http.delete(`/v1/jobs/${ID}`, () => HttpResponse.json({ ...JOB, status: 'cancelled' })),
  )
  // Mount useJobStats so the ['job-stats'] query has an active observer;
  // invalidateQueries only refetches active (observed) queries by default, so a
  // bare fetchQuery seed (no observer) would never refetch and the test would
  // pass vacuously regardless of whether ['job-stats'] is invalidated.
  const { result: stats } = renderHook(() => useJobStats(100_000), {
    wrapper: ({ children }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>,
  })
  await waitFor(() => expect(stats.current.status).toBe('success'))
  expect(statsCalls).toBe(1)

  render(
    <QueryClientProvider client={client}>
      <JobActions job={JOB} />
    </QueryClientProvider>,
  )
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Cancel job' }))

  // ['job-stats'] refetches -> statsCalls goes to 2. A two-key invalidation
  // (missing ['job-stats']) leaves it at 1 and fails this assertion.
  await waitFor(() => expect(statsCalls).toBe(2))
})

test('a done job shows Retry failed and Retry all, and no cancel pills', () => {
  renderActions({ ...JOB, status: 'done' })
  expect(screen.getByRole('button', { name: 'Retry failed' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Retry all' })).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Cancel' })).not.toBeInTheDocument()
})

test('a failed job shows the retry pills AND the cancel pills (server allows both)', () => {
  renderActions({ ...JOB, status: 'failed' })
  expect(screen.getByRole('button', { name: 'Retry failed' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Retry all' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
})

test('a running job shows NO retry pills (the server 409s an unfinished job)', () => {
  renderActions(JOB)
  expect(screen.queryByRole('button', { name: 'Retry failed' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Retry all' })).not.toBeInTheDocument()
})

test('a cancelled job shows NO retry pills (the server refuses it permanently)', () => {
  renderActions({ ...JOB, status: 'cancelled' })
  expect(screen.queryByRole('button', { name: 'Retry failed' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Retry all' })).not.toBeInTheDocument()
})

test('Retry failed confirms first, then POSTs ?task=failed', async () => {
  let task: string | null = null
  server.use(
    http.post(`/v1/jobs/${ID}/retry`, ({ request }) => {
      task = new URL(request.url).searchParams.get('task')
      return HttpResponse.json({ tasks_retried: 2 })
    }),
  )
  renderActions({ ...JOB, status: 'failed' })
  await userEvent.click(screen.getByRole('button', { name: 'Retry failed' }))
  await userEvent.click(screen.getByRole('button', { name: 'Retry failed tasks' }))
  await waitFor(() => expect(task).toBe('failed'))
})

test('Retry all confirms first, then POSTs ?task=all', async () => {
  let task: string | null = null
  server.use(
    http.post(`/v1/jobs/${ID}/retry`, ({ request }) => {
      task = new URL(request.url).searchParams.get('task')
      return HttpResponse.json({ tasks_retried: 5 })
    }),
  )
  renderActions({ ...JOB, status: 'done' })
  await userEvent.click(screen.getByRole('button', { name: 'Retry all' }))
  await userEvent.click(screen.getByRole('button', { name: 'Retry all tasks' }))
  await waitFor(() => expect(task).toBe('all'))
})

test('dismissing the retry confirm dialog fires no request', async () => {
  let hits = 0
  server.use(
    http.post(`/v1/jobs/${ID}/retry`, () => {
      hits++
      return HttpResponse.json({ tasks_retried: 1 })
    }),
  )
  renderActions({ ...JOB, status: 'done' })
  await userEvent.click(screen.getByRole('button', { name: 'Retry all' }))
  await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Cancel' }))
  await new Promise((r) => setTimeout(r, 20))
  expect(hits).toBe(0)
})

test('the retry confirm copy names what each mode re-runs', async () => {
  renderActions({ ...JOB, status: 'failed' })
  await userEvent.click(screen.getByRole('button', { name: 'Retry failed' }))
  const dialog = screen.getByRole('dialog')
  // "failed" silently includes timed_out server-side; the copy must say so.
  expect(within(dialog).getByText(/timed out/i)).toBeInTheDocument()
})

// Helper: click Retry failed and confirm it, for the error-surface tests below.
async function retryFailedAndConfirm(status: 'done' | 'failed' = 'failed') {
  renderActions({ ...JOB, status })
  await userEvent.click(screen.getByRole('button', { name: 'Retry failed' }))
  await userEvent.click(screen.getByRole('button', { name: 'Retry failed tasks' }))
}

test('a nothing-matched 409 says nothing changed, not a generic failure', async () => {
  server.use(
    http.post(`/v1/jobs/${ID}/retry`, () =>
      HttpResponse.json(
        { error: 'no tasks matched task=failed; this job has no failed or timed_out tasks' },
        { status: 409 },
      ),
    ),
  )
  await retryFailedAndConfirm()
  expect(await screen.findByText(/no failed or timed_out tasks/)).toBeInTheDocument()
  expect(screen.getByText('Nothing was changed.')).toBeInTheDocument()
})

test('a blocked-by-dependents 409 points the operator at Retry all', async () => {
  server.use(
    http.post(`/v1/jobs/${ID}/retry`, () =>
      HttpResponse.json(
        {
          error:
            'no tasks were reopened: a selected task has dependents that have already run, ' +
            'or the job changed while the request was in flight; nothing was applied',
        },
        { status: 409 },
      ),
    ),
  )
  await retryFailedAndConfirm()
  expect(await screen.findByText(/dependents that have already run/)).toBeInTheDocument()
  expect(screen.getByText(/Retry all also reopens/)).toBeInTheDocument()
})

test('a raced 409 tells the operator to repeat the action', async () => {
  server.use(
    http.post(`/v1/jobs/${ID}/retry`, () =>
      HttpResponse.json(
        { error: 'the job changed while the retry was in flight; nothing was applied - try again' },
        { status: 409 },
      ),
    ),
  )
  await retryFailedAndConfirm()
  expect(await screen.findByText(/nothing was applied - try again/)).toBeInTheDocument()
  expect(screen.getByText(/Retry the action\./)).toBeInTheDocument()
})

test('the retry error is visible with NO dialog mounted over it', async () => {
  server.use(
    http.post(`/v1/jobs/${ID}/retry`, () =>
      HttpResponse.json(
        { error: 'the job changed while the retry was in flight; nothing was applied - try again' },
        { status: 409 },
      ),
    ),
  )
  await retryFailedAndConfirm()
  await screen.findByText(/nothing was applied - try again/)
  // An error rendered while the dialog is still open sits behind its own scrim
  // and the button reads as doing nothing. The dialog must already be gone.
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  // Still on the page: the pills are mounted, so the action can be repeated.
  expect(screen.getByRole('button', { name: 'Retry failed' })).toBeInTheDocument()
})

test('a successful retry reports how many tasks were re-queued', async () => {
  server.use(http.post(`/v1/jobs/${ID}/retry`, () => HttpResponse.json({ tasks_retried: 3 })))
  await retryFailedAndConfirm()
  expect(await screen.findByText('Retried 3 tasks.')).toBeInTheDocument()
})

test('a single retried task is reported in the singular', async () => {
  server.use(http.post(`/v1/jobs/${ID}/retry`, () => HttpResponse.json({ tasks_retried: 1 })))
  await retryFailedAndConfirm()
  expect(await screen.findByText('Retried 1 task.')).toBeInTheDocument()
})

test('the stats query refetches after a successful retry (three-key invalidation)', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  let statsCalls = 0
  server.use(
    http.get('/v1/jobs/stats', () => {
      statsCalls++
      return HttpResponse.json({ running: 0, queued: 0, done_24h: 0, failed_24h: 0 })
    }),
    http.post(`/v1/jobs/${ID}/retry`, () => HttpResponse.json({ tasks_retried: 2 })),
  )
  // Mount useJobStats so ['job-stats'] has an ACTIVE observer; invalidateQueries
  // only refetches observed queries by default, so a bare fetchQuery seed would
  // make this pass vacuously.
  const { result: stats } = renderHook(() => useJobStats(100_000), {
    wrapper: ({ children }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>,
  })
  await waitFor(() => expect(stats.current.status).toBe('success'))
  expect(statsCalls).toBe(1)

  render(
    <QueryClientProvider client={client}>
      <JobActions job={{ ...JOB, status: 'failed' }} />
    </QueryClientProvider>,
  )
  await userEvent.click(screen.getByRole('button', { name: 'Retry failed' }))
  await userEvent.click(screen.getByRole('button', { name: 'Retry failed tasks' }))

  await waitFor(() => expect(statsCalls).toBe(2))
})

test('a 409 surfaces an inline error banner and does not navigate', async () => {
  server.use(
    http.delete(`/v1/jobs/${ID}`, () =>
      HttpResponse.json({ error: 'job is already in a terminal state' }, { status: 409 }),
    ),
  )
  renderActions(JOB)
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Cancel job' }))
  // The banner shows the server message (or the "409 <code>" fallback).
  expect(await screen.findByText(/terminal state|409/)).toBeInTheDocument()
  // The buttons remain mounted (no navigation, still on the detail page).
  expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
})
