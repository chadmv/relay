import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test } from 'vitest'
import { MemoryRouter, Route, Routes, useNavigate } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { server } from '../test/setup-helpers'
import { AuthProvider } from '../auth/AuthProvider'
import { clearToken, setToken } from '../lib/token'
import { WorkerDetailPage } from './WorkerDetailPage'

const ID = 'w1abc234'
const OTHER_ID = 'w2def567'
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
// Counts the requests the page makes to the tasks route. renderDetail owns the
// only handler for it, so a test can assert the poll never started.
let taskRequests = 0


// Every test needs a handler for /v1/workers/:id/tasks: the page mounts
// useWorkerTasks unconditionally (hooks run before the loading and error early
// returns), and setup.ts fails on an unhandled request. Registered here rather
// than per test so a test that does not care about tasks does not have to. The
// fixture is a hand-written literal, not the app's WorkerTasksPage type.
// Renders inside the router so a test can move the page from one worker id to
// the next, which is the only way to reach the keepPreviousData window.
function GoToOtherWorker() {
  const navigate = useNavigate()
  return (
    <button type="button" onClick={() => navigate(`/workers/${OTHER_ID}`)}>
      go to the other worker
    </button>
  )
}

function renderDetail(
  isAdmin: boolean,
  tasks: Record<string, unknown> = { items: [], next_cursor: '', total: 0 },
  tasksStatus = 200,
) {
  setToken('test-token')
  server.use(
    http.get('/v1/users/me', () =>
      HttpResponse.json({ id: 'u1', email: 'a@b.co', name: 'A', is_admin: isAdmin }),
    ),
    http.get(`/v1/workers/${ID}/tasks`, () => {
      taskRequests++
      return HttpResponse.json(tasks, { status: tasksStatus })
    }),
  )
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/workers/${ID}`]}>
        <AuthProvider>
          <GoToOtherWorker />
          <Routes>
            <Route path="/workers/:id" element={<WorkerDetailPage />} />
          </Routes>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => {
  clearToken()
  taskRequests = 0
})

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
  renderDetail(false, { items: [], next_cursor: '', total: 3 })
  expect(await screen.findByText('32c · 128G')).toBeInTheDocument()
  // Slots is real now: used comes from the tasks page total, which is the same
  // number the dispatcher treats as a used slot.
  expect(await screen.findByText('3 / 4')).toBeInTheDocument()
  expect(taskRequests).toBeGreaterThan(0)
})

test('the Slots KPI shows no used count until the tasks page actually loads', async () => {
  // A fabricated 0 reads as an idle worker. A failed tasks read says nothing
  // about how many slots are in use, so the card falls back to the placeholder
  // it carried before the endpoint existed.
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false, { error: 'boom' }, 500)
  // The worker heading is the positive control: the page rendered, so the Slots
  // card was reached.
  expect(await screen.findByText('render-rig-A')).toBeInTheDocument()
  await waitFor(() => expect(taskRequests).toBeGreaterThan(0))
  expect(screen.getByText('— / 4')).toBeInTheDocument()
  expect(screen.queryByText('0 / 4')).not.toBeInTheDocument()
  // The bar is the other half of the same card and must not contradict it.
  expect(screen.getByTestId('progress-fill').style.width).toBe('0%')
})

test('the Slots bar draws nothing while the tasks page belongs to the previous worker', async () => {
  // keepPreviousData hands worker B the page it fetched for worker A, so a bar
  // fed straight from `total` draws A's load under B's placeholder fraction.
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  server.use(
    http.get(`/v1/workers/${OTHER_ID}`, () => HttpResponse.json({ ...WORKER, id: OTHER_ID, name: 'render-rig-B' })),
  )
  server.use(http.get(`/v1/workers/${OTHER_ID}/metrics`, () => HttpResponse.json(metrics())))
  // B's tasks never answer, which is the whole window this test is about.
  server.use(http.get(`/v1/workers/${OTHER_ID}/tasks`, () => new Promise(() => {})))
  renderDetail(false, { items: [], next_cursor: '', total: 3 })

  expect(await screen.findByText('3 / 4')).toBeInTheDocument()
  expect(screen.getByTestId('progress-fill').style.width).toBe('75%')

  await userEvent.click(screen.getByRole('button', { name: 'go to the other worker' }))

  expect(await screen.findByText('render-rig-B')).toBeInTheDocument()
  expect(screen.getByText('— / 4')).toBeInTheDocument()
  expect(screen.getByTestId('progress-fill').style.width).toBe('0%')
})

test('a 404 worker stops the tasks and metrics polls', async () => {
  // Both hooks sit above the not-found early return, so without an enabled gate
  // they keep polling a worker the page has already given up on.
  let metricCalls = 0
  server.use(
    http.get(`/v1/workers/${ID}`, () =>
      HttpResponse.json({ error: 'worker not found' }, { status: 404 }),
    ),
  )
  server.use(
    http.get(`/v1/workers/${ID}/metrics`, () => {
      metricCalls++
      return HttpResponse.json(metrics({ samples: [] }))
    }),
  )
  renderDetail(false)
  expect(await screen.findByText('Worker not found.')).toBeInTheDocument()
  await new Promise((r) => setTimeout(r, 50))
  expect(taskRequests).toBe(0)
  expect(metricCalls).toBe(0)
})

test('the Slots progress bar clamps when used exceeds max_slots', async () => {
  // max_slots is a dispatcher input, not a database constraint: lowering it via
  // PATCH requeues nothing, so used > max is a reachable state and the fill must
  // not render above 100%.
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false, { items: [], next_cursor: '', total: 6 })
  expect(await screen.findByText('6 / 4')).toBeInTheDocument()
  const fills = screen.getAllByTestId('progress-fill')
  expect(fills).toHaveLength(1)
  expect(fills[0].style.width).toBe('100%')
})

test('renders the Jobs-today placeholder KPI with no fabricated data', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false)
  expect(await screen.findByText('activity endpoint pending')).toBeInTheDocument()
  // Guard against a fabricated count like the hi-fi mock's "47".
  expect(screen.queryByText('47')).not.toBeInTheDocument()
})

test('the GPU KPI card renders no fabricated telemetry sub-string', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  renderDetail(false)
  await screen.findByText('2 × RTX 4090')
  expect(screen.queryByText(/cuda 12\.3/i)).not.toBeInTheDocument()
  expect(screen.queryByText(/nvidia-smi/i)).not.toBeInTheDocument()
})

test('the reservations panel contains no fabricated reservation rows', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  server.use(http.get(`/v1/workers/${ID}/workspaces`, () => HttpResponse.json([])))
  renderDetail(true)
  expect(await screen.findByText('no per-worker reservation lookup yet')).toBeInTheDocument()
  // Identified by accessible name rather than asserted absent. Both real tables
  // on an admin's page are empty here, so each contributes only its header row.
  // A fabricated reservations table would show up as a third table or as an
  // extra row.
  const tables = screen.getAllByRole('table')
  expect(tables.map((el) => el.getAttribute('aria-label')).sort()).toEqual([
    'Current tasks',
    'Source workspaces',
  ])
  expect(screen.getAllByRole('row')).toHaveLength(2)
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

test('admins can add a label inline without opening the full Edit dialog', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  server.use(http.get(`/v1/workers/${ID}/workspaces`, () => HttpResponse.json([])))
  renderDetail(true)
  await userEvent.click(await screen.findByRole('button', { name: /add label/i }))
  // Inline input appears, not the full name/max-slots Edit form.
  expect(screen.getByRole('textbox')).toBeInTheDocument()
  expect(screen.queryByLabelText(/max slots/i)).not.toBeInTheDocument()
})

test('the header Edit pill still opens the name/slots form (not label editing)', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  server.use(http.get(`/v1/workers/${ID}/workspaces`, () => HttpResponse.json([])))
  renderDetail(true)
  await userEvent.click(await screen.findByRole('button', { name: 'Edit' }))
  expect(screen.getByLabelText(/^name$/i)).toBeInTheDocument()
  expect(screen.getByLabelText(/max slots/i)).toBeInTheDocument()
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

test('every table on the page is named by its own panel title', async () => {
  server.use(http.get(`/v1/workers/${ID}`, () => HttpResponse.json(WORKER)))
  server.use(http.get(`/v1/workers/${ID}/metrics`, () => HttpResponse.json(metrics())))
  server.use(http.get(`/v1/workers/${ID}/workspaces`, () => HttpResponse.json([])))
  renderDetail(true)
  await screen.findByText('no per-worker reservation lookup yet')

  const tables = screen.getAllByRole('table')
  // The count assertion is not decoration. Without it the loop below passes
  // vacuously the moment a panel stops rendering, or the moment Panel stops
  // publishing the attribute - which is the same failure the Panel unit test
  // catches from the other side, deliberately.
  expect(tables).toHaveLength(2)
  for (const table of tables) {
    const panel = table.closest('[data-panel-title]')
    expect(panel).not.toBeNull()
    expect(table).toHaveAccessibleName(panel!.getAttribute('data-panel-title')!)
  }
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
