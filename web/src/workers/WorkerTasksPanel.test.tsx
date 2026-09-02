import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import type { ReactElement } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { WorkerTasksPanel } from './WorkerTasksPanel'

// Hand-written JSON, deliberately independent of the WorkerTask type: a fixture
// built from the app's own type agrees with the decoder by construction. env and
// requires are {} rather than null because rawObject (internal/api/server.go)
// normalizes both empty and the literal null before the row reaches the wire,
// and the timestamps carry a local offset rather than a Z suffix, which is what
// the Go handler emits.
const RUNNING = {
  id: 't1',
  name: 'render-shot-042',
  status: 'running',
  commands: [['blender', '-b', 'shot042.blend']],
  env: {},
  requires: {},
  timeout_seconds: 3600,
  retries: 2,
  retry_count: 1,
  worker_id: 'w1',
  job_id: 'j1',
  job_name: 'nightly-render',
  assigned_at: '2026-09-01T09:14:02.118-07:00',
  started_at: '2026-09-01T09:16:40.902-07:00',
}

// A dispatched row carries no started_at KEY at all - not a null, and not a zero
// timestamp.
const DISPATCHED = {
  id: 't2',
  name: 'sync-depot',
  status: 'dispatched',
  commands: [['p4', 'sync']],
  env: {},
  requires: {},
  timeout_seconds: null,
  retries: 0,
  retry_count: 0,
  worker_id: 'w1',
  job_id: 'j1',
  job_name: 'nightly-render',
  assigned_at: '2026-09-01T09:13:55.004-07:00',
}

function renderPanel(ui: ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>{ui}</MemoryRouter>
    </QueryClientProvider>,
  )
}

function tasksPage(items: unknown[], total = items.length) {
  return { items, next_cursor: '', total }
}

test('renders a row per assigned task with links to the job and the task log', async () => {
  server.use(
    http.get('/v1/workers/w1/tasks', () => HttpResponse.json(tasksPage([RUNNING, DISPATCHED]))),
  )
  renderPanel(<WorkerTasksPanel workerId="w1" />)

  expect(await screen.findByText('render-shot-042')).toBeInTheDocument()
  const table = screen.getByRole('table')
  expect(table).toHaveAccessibleName('Current tasks')
  // Header row plus one row per task.
  expect(within(table).getAllByRole('row')).toHaveLength(3)

  expect(screen.getByRole('link', { name: 'render-shot-042' })).toHaveAttribute(
    'href',
    '/jobs/j1/tasks/t1',
  )
  expect(screen.getAllByRole('link', { name: 'nightly-render' })[0]).toHaveAttribute(
    'href',
    '/jobs/j1',
  )
  expect(screen.getByText('running')).toBeInTheDocument()
  // retries > 0 renders the fraction; retries === 0 renders a single hyphen.
  expect(screen.getByText('1/2')).toBeInTheDocument()
  expect(screen.getByText('-')).toBeInTheDocument()
})

test('shows an empty state when the worker has no active tasks', async () => {
  server.use(http.get('/v1/workers/w1/tasks', () => HttpResponse.json(tasksPage([]))))
  renderPanel(<WorkerTasksPanel workerId="w1" />)

  expect(await screen.findByText('No active tasks.')).toBeInTheDocument()
  // The header row still renders; there is no data row.
  expect(within(screen.getByRole('table')).getAllByRole('row')).toHaveLength(1)
})

test('shows a loading line before the first page arrives', async () => {
  server.use(http.get('/v1/workers/w1/tasks', () => HttpResponse.json(tasksPage([RUNNING]))))
  renderPanel(<WorkerTasksPanel workerId="w1" />)

  expect(screen.getByText('loading tasks...')).toBeInTheDocument()
  await screen.findByText('render-shot-042')
  expect(screen.queryByText('loading tasks...')).not.toBeInTheDocument()
})

test('shows the error and a Retry inside the panel', async () => {
  let calls = 0
  server.use(
    http.get('/v1/workers/w1/tasks', () => {
      calls++
      return HttpResponse.json({ error: 'boom' }, { status: 500 })
    }),
  )
  renderPanel(<WorkerTasksPanel workerId="w1" />)

  // apiFetch builds the ApiError message as status plus the error envelope's
  // code, so the banner renders both; asserting the bare code would pass on a
  // panel that dropped the status.
  expect(await screen.findByText('500 boom')).toBeInTheDocument()
  // The panel owns its own error surface; it must not be empty-stated as well.
  expect(screen.queryByText('No active tasks.')).not.toBeInTheDocument()
  const before = calls
  await userEvent.click(screen.getByRole('button', { name: /retry/i }))
  await waitFor(() => expect(calls).toBeGreaterThan(before))
})

test('renders a dispatched task with no start time', async () => {
  server.use(http.get('/v1/workers/w1/tasks', () => HttpResponse.json(tasksPage([DISPATCHED]))))
  renderPanel(<WorkerTasksPanel workerId="w1" />)

  expect(await screen.findByText('sync-depot')).toBeInTheDocument()
  expect(screen.getByText('not started')).toBeInTheDocument()
  expect(screen.queryByText(/Invalid Date/)).not.toBeInTheDocument()
  expect(screen.queryByText(/1970/)).not.toBeInTheDocument()
  expect(screen.queryByText('NaN')).not.toBeInTheDocument()
})

test('renders no progress affordance', async () => {
  // relay has no progress column, no progress field on any proto message and no
  // agent-side computation of one. The hi-fi's per-task bar has nothing behind it.
  server.use(
    http.get('/v1/workers/w1/tasks', () => HttpResponse.json(tasksPage([RUNNING, DISPATCHED]))),
  )
  renderPanel(<WorkerTasksPanel workerId="w1" />)

  await screen.findByText('render-shot-042')
  expect(screen.queryByRole('progressbar')).not.toBeInTheDocument()
  expect(screen.queryByTestId('progress-fill')).not.toBeInTheDocument()
  expect(screen.queryByText(/\d+%/)).not.toBeInTheDocument()
})
