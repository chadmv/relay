import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { openSseResponse } from '../test/sseStream'
import { clearToken, setToken } from '../lib/token'
import { AppRoutes } from '../app/router'
import { AuthProvider } from '../auth/AuthProvider'

const JOB = {
  id: 'j1',
  name: 'shot-042 render',
  priority: 'high',
  status: 'running',
  submitted_by: 'u1',
  labels: null,
  created_at: '2026-08-09T00:00:00Z',
  updated_at: '2026-08-09T00:00:00Z',
  tasks: [
    {
      id: 't1', name: 'frame-001', status: 'running',
      commands: [['blender', '-b']], env: null, requires: null,
      timeout_seconds: null, retries: 1, retry_count: 0, worker_id: 'w1abcdef',
    },
  ],
}

function renderRoute(path: string) {
  setToken('test-token')
  server.use(http.get('/v1/users/me', () => HttpResponse.json({ id: 'u1', email: 'a@b.co', name: 'A', is_admin: false })))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <AuthProvider>
          <AppRoutes />
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => clearToken())

test('the /jobs/:id/tasks/:taskId route renders the header and tails the task', async () => {
  server.use(http.get('/v1/jobs/j1', () => HttpResponse.json(JOB)))
  server.use(
    http.get('/v1/tasks/t1/logs', () =>
      HttpResponse.json({
        items: [{ seq: 1, stream: 'stdout', content: 'rendering\n', created_at: '2026-08-09T14:36:25Z' }],
        next_seq: 0,
        prev_seq: 0,
        total: 1,
      }),
    ),
  )
  server.use(http.get('/v1/events', () => openSseResponse()))

  renderRoute('/jobs/j1/tasks/t1')
  expect(await screen.findByText('frame-001')).toBeInTheDocument()
  expect(await screen.findByText('rendering')).toBeInTheDocument()
  // Hi-fi chrome (hifi3-holo-pages.jsx:2716-2745): breadcrumb, status pill,
  // worker, endpoint caption, LIVE badge, follow-tail.
  expect(screen.getByRole('link', { name: /job detail/i })).toHaveAttribute('href', '/jobs/j1')
  expect(screen.getByText('running')).toBeInTheDocument()
  expect(screen.getByText(/w1abcd/)).toBeInTheDocument()
  expect(screen.getByText('/v1/events?task_id=t1 · single-task stream')).toBeInTheDocument()
  expect(screen.getByText('LIVE')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /follow tail/i })).toBeInTheDocument()
  // The Download button of the hi-fi is deliberately omitted (spec, Omissions).
  expect(screen.queryByRole('button', { name: /download/i })).toBeNull()
})

test('a task id that is not in the job renders a not-found panel and opens no stream', async () => {
  let streamCount = 0
  server.use(http.get('/v1/jobs/j1', () => HttpResponse.json(JOB)))
  server.use(http.get('/v1/events', () => { streamCount++; return openSseResponse() }))
  server.use(http.get('/v1/tasks/:tid/logs', () => HttpResponse.json({ items: [], next_seq: 0, prev_seq: 0, total: 0 })))

  renderRoute('/jobs/j1/tasks/does-not-exist')
  expect(await screen.findByText(/task not found in this job/i)).toBeInTheDocument()
  expect(screen.getByRole('link', { name: /job detail/i })).toBeInTheDocument()
  await new Promise((r) => setTimeout(r, 60))
  expect(streamCount).toBe(0)
})
