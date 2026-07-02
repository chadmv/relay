import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test } from 'vitest'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { server } from '../test/setup-helpers'
import { AuthProvider } from '../auth/AuthProvider'
import { clearToken, setToken } from '../lib/token'
import { JobDetailPage } from './JobDetailPage'

const ID = 'j1'

const JOB = {
  id: ID,
  name: 'shot-042 render',
  priority: 'high',
  status: 'running',
  submitted_by: 'u1',
  submitted_by_email: 'mira@studio.dev',
  labels: { team: 'fx' },
  created_at: '2026-07-01T00:00:00Z',
  updated_at: '2026-07-01T00:01:00Z',
  tasks: [
    {
      id: 't1', name: 'frame-001', status: 'done',
      commands: [['blender', '-b']], env: {}, requires: {},
      timeout_seconds: 3600, retries: 2, retry_count: 0,
    },
    {
      id: 't2', name: 'denoise', status: 'running',
      commands: [['denoise', '--all']], env: { CUDA: '1' }, requires: { gpu: 'true' },
      timeout_seconds: null, retries: 1, retry_count: 0, depends_on: ['frame-001'],
    },
  ],
}

function renderDetail(me = { id: 'u1', email: 'a@b.co', name: 'A', is_admin: false }) {
  setToken('test-token')
  server.use(http.get('/v1/users/me', () => HttpResponse.json(me)))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/jobs/${ID}`]}>
        <AuthProvider>
          <Routes>
            <Route path="/jobs/:id" element={<JobDetailPage />} />
          </Routes>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => clearToken())

test('renders job identity and its tasks', async () => {
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json(JOB)))
  renderDetail()
  expect(await screen.findByText('shot-042 render')).toBeInTheDocument()
  // The meta line interleaves plain text with the email in one div, so the
  // composite text node only matches a regex, not an exact string (mirrors
  // WorkerDetailPage.test.tsx's hostname assertion).
  expect(screen.getByText(/mira@studio\.dev/)).toBeInTheDocument()
  // 'frame-001' appears twice: the task row name and denoise's deps cell.
  expect(screen.getAllByText('frame-001').length).toBeGreaterThan(0)
  // 'denoise' appears twice: the task row name and (denoise is the default
  // selected running task) its command line "$ denoise --all" in the Spec pane.
  expect(screen.getAllByText('denoise').length).toBeGreaterThan(0)
})

test('shows not-found for a 404 job with a back link', async () => {
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json({ error: 'job not found' }, { status: 404 })))
  renderDetail()
  expect(await screen.findByText('Job not found.')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: /jobs/i })).toBeInTheDocument()
})

test('shows a generic error with a Retry button on a non-404 failure', async () => {
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json({ error: 'boom' }, { status: 500 })))
  renderDetail()
  expect(await screen.findByRole('button', { name: /retry/i })).toBeInTheDocument()
})

test('defaults to the Spec tab and shows the selected task spec', async () => {
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json(JOB)))
  renderDetail()
  // Default selection is the first running task (denoise), Spec tab active.
  expect(await screen.findByText(/denoise --all/)).toBeInTheDocument()
  expect(screen.getByText(/CUDA/)).toBeInTheDocument()
})

test('does NOT hit the log endpoint while the Spec tab is active', async () => {
  let logCount = 0
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json(JOB)))
  server.use(
    http.get('/v1/tasks/:tid/logs', () => {
      logCount++
      return HttpResponse.json({ items: [], next_seq: 0, total: 0 })
    }),
  )
  renderDetail()
  await screen.findByText('shot-042 render')
  await new Promise((r) => setTimeout(r, 60))
  expect(logCount).toBe(0)
})

test('switching to the Log tab fetches once and renders lines', async () => {
  let logCount = 0
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json(JOB)))
  server.use(
    http.get('/v1/tasks/t2/logs', () => {
      logCount++
      return HttpResponse.json({
        items: [{ seq: 1, stream: 'stdout', content: 'rendering', created_at: '2026-07-01T00:00:00Z' }],
        next_seq: 0,
        total: 1,
      })
    }),
  )
  renderDetail()
  await screen.findByText('shot-042 render')
  await userEvent.click(screen.getByRole('tab', { name: /log/i }))
  expect(await screen.findByText('rendering')).toBeInTheDocument()
  expect(logCount).toBe(1)
})

test('selecting a task updates aria-selected and drives the spec pane', async () => {
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json(JOB)))
  renderDetail()
  await screen.findByText('shot-042 render')
  // 'frame-001' appears twice (task row name + denoise's deps cell); click the
  // row's name cell specifically to select the frame-001 task.
  const rows = screen.getAllByRole('row')
  const frameRow = rows.find((r) => r.getAttribute('role') === 'row' && r.textContent?.startsWith('frame-001'))!
  await userEvent.click(frameRow)
  const selectedAfter = screen.getAllByRole('row').filter((r) => r.getAttribute('aria-selected') === 'true')
  expect(selectedAfter[0]).toHaveTextContent('frame-001')
  expect(screen.getByText(/blender -b/)).toBeInTheDocument()
})

test('owner (non-admin) sees the cancel actions in the reserved slot', async () => {
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json(JOB)))
  // Default me handler is id:'u1', is_admin:false; JOB.submitted_by is 'u1'.
  renderDetail()
  await screen.findByText('shot-042 render')
  const slot = screen.getByTestId('job-actions')
  expect(within(slot).getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
  expect(within(slot).getByRole('button', { name: 'Force cancel' })).toBeInTheDocument()
})

test('admin non-owner sees the cancel actions', async () => {
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json(JOB)))
  // renderDetail() registers its /v1/users/me handler internally before
  // AuthProvider's mount effect fires the request, so the user must be passed
  // in rather than overridden with a second server.use() call (which would
  // race the synchronous effect flush inside render()).
  renderDetail({ id: 'other', email: 'a@b.co', name: 'A', is_admin: true })
  await screen.findByText('shot-042 render')
  const slot = screen.getByTestId('job-actions')
  expect(within(slot).getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
  expect(within(slot).getByRole('button', { name: 'Force cancel' })).toBeInTheDocument()
})

test('non-owner non-admin does NOT see the cancel actions', async () => {
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json(JOB)))
  renderDetail({ id: 'other', email: 'a@b.co', name: 'A', is_admin: false })
  await screen.findByText('shot-042 render')
  const slot = screen.getByTestId('job-actions')
  expect(within(slot).queryByRole('button', { name: 'Cancel' })).not.toBeInTheDocument()
  expect(within(slot).queryByRole('button', { name: 'Force cancel' })).not.toBeInTheDocument()
})

test('derives progress from the tasks array (1 of 2 done)', async () => {
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json(JOB)))
  renderDetail()
  expect(await screen.findByText(/1\s*\/\s*2 tasks done/i)).toBeInTheDocument()
})

test('does not fabricate unbacked timing or the live-log affordances', async () => {
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json(JOB)))
  renderDetail()
  await screen.findByText('shot-042 render')
  // Omitted per spec (no backend field / SSE blocked): elapsed, ETA, and a
  // Retry/Abort header pill are not rendered. A dead control reads as broken.
  expect(screen.queryByText(/elapsed/i)).toBeNull()
  expect(screen.queryByText(/\beta\b/i)).toBeNull()
  expect(screen.queryByRole('button', { name: /^abort$/i })).toBeNull()
  expect(screen.queryByRole('button', { name: /^retry$/i })).toBeNull() // no 404 -> no Retry
})

test('the Log tab shows a static/history marker, not a LIVE badge', async () => {
  server.use(http.get(`/v1/jobs/${ID}`, () => HttpResponse.json(JOB)))
  server.use(
    http.get('/v1/tasks/t2/logs', () =>
      HttpResponse.json({
        items: [{ seq: 1, stream: 'stdout', content: 'rendering', created_at: '2026-07-01T00:00:00Z' }],
        next_seq: 0,
        total: 1,
      }),
    ),
  )
  renderDetail()
  await screen.findByText('shot-042 render')
  await userEvent.click(screen.getByRole('tab', { name: /log/i }))
  await screen.findByText('rendering')
  expect(screen.getByText(/static|history/i)).toBeInTheDocument()
  expect(screen.queryByText(/^live$/i)).toBeNull()
})
