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
