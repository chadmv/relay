import { render, screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test } from 'vitest'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { server } from '../test/setup-helpers'
import { AuthProvider } from '../auth/AuthProvider'
import { clearToken, setToken } from '../lib/token'
import { WorkerDetailPage } from './WorkerDetailPage'

const ID = 'w1abc234'
const GB = 1024 ** 3

const WORKER = {
  id: ID,
  name: 'render-rig-A',
  hostname: 'render-a.studio.dev',
  cpu_cores: 32,
  ram_gb: 128,
  gpu_count: 2,
  gpu_model: 'RTX 4090',
  os: 'linux',
  max_slots: 4,
  labels: { rack: 'A' },
  status: 'online',
  last_seen_at: '2026-06-05T00:00:00Z',
  last_sample_at: '2026-06-05T00:00:00Z',
}

function metrics(over: Record<string, unknown> = {}) {
  return {
    worker_id: ID,
    sample_interval_seconds: 10,
    samples: [
      { t: '2026-06-05T00:00:00Z', cpu_pct: 40, mem_used: 64 * GB, mem_total: 128 * GB, gpu: true, gpu_util_pct: 55, gpu_mem_used: 8 * GB, gpu_mem_total: 24 * GB },
      { t: '2026-06-05T00:00:10Z', cpu_pct: 60, mem_used: 70 * GB, mem_total: 128 * GB, gpu: true, gpu_util_pct: 70, gpu_mem_used: 9 * GB, gpu_mem_total: 24 * GB },
    ],
    ...over,
  }
}

function renderDetail(isAdmin: boolean) {
  setToken('test-token')
  server.use(
    http.get('/v1/users/me', () =>
      HttpResponse.json({ id: 'u1', email: 'a@b.co', name: 'A', is_admin: isAdmin }),
    ),
  )
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/workers/${ID}`]}>
        <AuthProvider>
          <Routes>
            <Route path="/workers/:id" element={<WorkerDetailPage />} />
          </Routes>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => clearToken())

test('renders the breadcrumb, worker name, and identity sub-line', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false)
  expect(await screen.findByRole('link', { name: /workers/i })).toBeInTheDocument()
  expect(screen.getByText('render-rig-A')).toBeInTheDocument()
  expect(screen.getByText(/render-a\.studio\.dev/)).toBeInTheDocument()
})

test('renders the CPU/RAM and Slots KPI cards', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false)
  expect(await screen.findByText('32c · 128G')).toBeInTheDocument()
  // Slots: no active-slots field exists yet, so used renders as an em dash.
  expect(screen.getByText('— / 4')).toBeInTheDocument()
})

test('renders the Jobs-today placeholder KPI with no fabricated data', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false)
  expect(await screen.findByText('activity endpoint pending')).toBeInTheDocument()
  // Guard against a fabricated count like the hi-fi mock's "47".
  expect(screen.queryByText('47')).not.toBeInTheDocument()
})

test('renders the current-tasks placeholder note, not an empty table', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false)
  expect(await screen.findByText('no per-worker task feed yet')).toBeInTheDocument()
})

test('the GPU KPI card renders no fabricated telemetry sub-string', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false)
  await screen.findByText('2 × RTX 4090')
  expect(screen.queryByText(/cuda 12\.3/i)).not.toBeInTheDocument()
  expect(screen.queryByText(/nvidia-smi/i)).not.toBeInTheDocument()
})

test('the current-tasks panel contains no fabricated task rows', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false)
  await screen.findByText('no per-worker task feed yet')
  expect(screen.queryByRole('row')).not.toBeInTheDocument()
  expect(screen.queryByRole('table')).not.toBeInTheDocument()
})

test('the reservations panel contains no fabricated reservation rows', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  server.use(http.get(`/v1/workers/${ID}/workspaces`, () => HttpResponse.json([])))
  renderDetail(true)
  expect(await screen.findByText('no per-worker reservation lookup yet')).toBeInTheDocument()
  expect(screen.queryByRole('row')).not.toBeInTheDocument()
  expect(screen.queryByRole('table')).not.toBeInTheDocument()
})

test('renders CPU/memory telemetry charts', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false)
  expect(await screen.findByRole('img', { name: 'CPU' })).toBeInTheDocument()
  expect(screen.getByRole('img', { name: 'MEMORY' })).toBeInTheDocument()
})

test('shows GPU charts when the worker has a GPU', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false)
  expect(await screen.findByRole('img', { name: 'GPU' })).toBeInTheDocument()
  expect(screen.getByRole('img', { name: 'GPU MEMORY' })).toBeInTheDocument()
})

test('hides GPU charts when the worker has no GPU', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json({ ...WORKER, gpu_count: 0, gpu_model: '' })))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false)
  expect(await screen.findByRole('img', { name: 'CPU' })).toBeInTheDocument()
  expect(screen.queryByRole('img', { name: 'GPU' })).not.toBeInTheDocument()
})

test('shows an empty telemetry state when there are no samples', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics({ samples: [] }))))
  renderDetail(false)
  expect(await screen.findByText('No telemetry yet.')).toBeInTheDocument()
})

test('non-admins still see the header status indicator', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false)
  await screen.findByText('render-rig-A')
  expect(screen.getByText('ONLINE')).toBeInTheDocument()
})

test('renders read-only labels for non-admins', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false)
  expect(await screen.findByText('rack=A')).toBeInTheDocument()
  // Non-admins get no add-label affordance.
  expect(screen.queryByRole('button', { name: /add label/i })).not.toBeInTheDocument()
})

test('shows not-found for a 404 worker', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json({ error: 'worker not found' }, { status: 404 })))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics({ samples: [] }))))
  renderDetail(false)
  expect(await screen.findByText('Worker not found.')).toBeInTheDocument()
})

test('shows a generic error with a Retry button for a non-404 failure', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json({ error: 'boom' }, { status: 500 })))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics({ samples: [] }))))
  renderDetail(false)
  expect(await screen.findByRole('button', { name: /retry/i })).toBeInTheDocument()
})

test('admins see the action bar, the Source workspaces panel, and the reservations placeholder', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  server.use(
    http.get(`/v1/workers/${ID}/workspaces`, () =>
      HttpResponse.json([
        { source_type: 'perforce', source_key: '//depot/x', short_id: 'ws-a4f2', baseline_hash: '@1', last_used_at: '2026-06-05T00:00:00Z' },
      ]),
    ),
  )
  renderDetail(true)
  expect(await screen.findByRole('button', { name: 'Edit' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Disable' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Revoke' })).toBeInTheDocument()
  expect(await screen.findByText('ws-a4f2')).toBeInTheDocument()
  expect(screen.getByText('no per-worker reservation lookup yet')).toBeInTheDocument()
})

test('non-admins see none of the action controls and never fetch workspaces', async () => {
  let wsCount = 0
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  server.use(
    http.get(`/v1/workers/${ID}/workspaces`, () => {
      wsCount++
      return HttpResponse.json([])
    }),
  )
  renderDetail(false)
  await screen.findByText('render-rig-A')
  await screen.findByRole('img', { name: 'CPU' })
  await new Promise((r) => setTimeout(r, 50))
  expect(wsCount).toBe(0)
  expect(screen.queryByRole('button', { name: 'Edit' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Disable' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Revoke' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /evict/i })).not.toBeInTheDocument()
  // Admin-only right-column pieces are hidden.
  expect(screen.queryByText('no per-worker reservation lookup yet')).not.toBeInTheDocument()
  expect(screen.queryByText(/Long-lived agent token/)).not.toBeInTheDocument()
})
