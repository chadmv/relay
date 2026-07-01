import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { server } from '../test/setup-helpers'
import { WorkerActions } from './WorkerActions'
import { useWorker } from './useWorker'
import type { Worker } from './api'

const ID = 'w1'

const WORKER: Worker = {
  id: ID,
  name: 'rig-A',
  hostname: 'h',
  cpu_cores: 8,
  ram_gb: 32,
  gpu_count: 0,
  gpu_model: '',
  os: 'linux',
  max_slots: 2,
  labels: null,
  status: 'online',
}

function LocationProbe() {
  return <div data-testid="loc">{useLocation().pathname}</div>
}

function renderActions(worker: Worker) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/workers/${ID}`]}>
        <Routes>
          <Route path="/workers/:id" element={<><WorkerActions worker={worker} /><LocationProbe /></>} />
          <Route path="/workers" element={<LocationProbe />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

// Mirrors WorkerDetailPage: worker comes from useWorker so a mutation's
// invalidate-on-success refetch drives the prop, letting Disable/Drain vs.
// Enable actually toggle across a mutation like it does in the real app.
function LiveWorkerActions() {
  const { data: worker } = useWorker(ID, 0)
  if (!worker) return null
  return <WorkerActions worker={worker} />
}

// Stateful GET handler: reflects whatever the most recent disable/enable
// response set, so a query invalidation after a mutation refetches the
// post-mutation worker (mirroring the real server).
function renderLiveActions(initial: Worker) {
  let current = initial
  server.use(
    http.get(`/v1/workers/${ID}`, () => HttpResponse.json(current)),
    http.post(`/v1/workers/${ID}/disable`, () => {
      current = { ...current, status: 'disabled', disabled_at: 'now' }
      return HttpResponse.json({ ...current, requeued_tasks: 3 })
    }),
    http.post(`/v1/workers/${ID}/enable`, () => {
      current = { ...current, status: 'online', disabled_at: undefined }
      return HttpResponse.json(current)
    }),
  )
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/workers/${ID}`]}>
        <Routes>
          <Route path="/workers/:id" element={<><LiveWorkerActions /><LocationProbe /></>} />
          <Route path="/workers" element={<LocationProbe />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

test('online worker shows Disable and Drain (not Enable)', () => {
  renderActions(WORKER)
  expect(screen.getByRole('button', { name: 'Disable' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Drain' })).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Enable' })).not.toBeInTheDocument()
})

test('disabled worker shows Enable (not Disable/Drain)', () => {
  renderActions({ ...WORKER, status: 'disabled', disabled_at: '2026-07-01T00:00:00Z' })
  expect(screen.getByRole('button', { name: 'Enable' })).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Disable' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Drain' })).not.toBeInTheDocument()
})

test('clicking Disable opens a confirm dialog; cancel fires no request', async () => {
  let hits = 0
  server.use(http.post(`/v1/workers/${ID}/disable`, () => { hits++; return HttpResponse.json({ ...WORKER, requeued_tasks: 0 }) }))
  renderActions(WORKER)
  await userEvent.click(screen.getByRole('button', { name: 'Disable' }))
  expect(screen.getByRole('dialog')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  await new Promise((r) => setTimeout(r, 20))
  expect(hits).toBe(0)
})

test('confirming Disable fires exactly one request to /disable', async () => {
  let hits = 0
  server.use(http.post(`/v1/workers/${ID}/disable`, () => { hits++; return HttpResponse.json({ ...WORKER, status: 'disabled', disabled_at: 'now', requeued_tasks: 0 }) }))
  renderActions(WORKER)
  await userEvent.click(screen.getByRole('button', { name: 'Disable' }))
  // The confirm button inside the dialog is also labelled "Disable"; scope to the dialog.
  const dialog = screen.getByRole('dialog')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Disable' }))
  await waitFor(() => expect(hits).toBe(1))
})

test('revoke success navigates to /workers', async () => {
  server.use(http.delete(`/v1/workers/${ID}/token`, () => new HttpResponse(null, { status: 204 })))
  renderActions(WORKER)
  await userEvent.click(screen.getByRole('button', { name: 'Revoke' }))
  const dialog = screen.getByRole('dialog')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Revoke' }))
  await waitFor(() => expect(screen.getByTestId('loc')).toHaveTextContent('/workers'))
})

test('a mutation error renders an inline message and leaves the actions mounted', async () => {
  server.use(http.post(`/v1/workers/${ID}/enable`, () => HttpResponse.json({ error: 'boom' }, { status: 500 })))
  renderActions({ ...WORKER, status: 'disabled', disabled_at: 'now' })
  await userEvent.click(screen.getByRole('button', { name: 'Enable' }))
  expect(await screen.findByText(/boom|500/)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Enable' })).toBeInTheDocument()
})

test('the requeued-tasks note clears after re-enabling the worker', async () => {
  renderLiveActions(WORKER)
  await userEvent.click(await screen.findByRole('button', { name: 'Drain' }))
  const dialog = screen.getByRole('dialog')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Drain' }))
  expect(await screen.findByText(/Requeued 3 task\(s\)/)).toBeInTheDocument()

  const enableButton = await screen.findByRole('button', { name: 'Enable' })
  await userEvent.click(enableButton)
  await waitFor(() => expect(screen.queryByText(/Requeued 3 task\(s\)/)).not.toBeInTheDocument())
})
