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

test('renders identity, hardware, and CPU/memory telemetry charts', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false)
  expect(await screen.findByText('render-rig-A')).toBeInTheDocument()
  expect(screen.getByText(/render-a\.studio\.dev/)).toBeInTheDocument()
  expect(screen.getByText('32c · 128GB')).toBeInTheDocument()
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

test('shows not-found for a 404 worker', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json({ error: 'worker not found' }, { status: 404 })))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics({ samples: [] }))))
  renderDetail(false)
  expect(await screen.findByText('Worker not found.')).toBeInTheDocument()
})

test('admins see the workspaces panel', async () => {
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
  expect(await screen.findByText('ws-a4f2')).toBeInTheDocument()
})

test('non-admins never see or fetch workspaces', async () => {
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
  expect(screen.queryByText('SOURCE WORKSPACES')).not.toBeInTheDocument()
})
