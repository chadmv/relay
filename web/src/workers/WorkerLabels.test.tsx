import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { server } from '../test/setup-helpers'
import { AuthProvider } from '../auth/AuthProvider'
import { clearToken, setToken } from '../lib/token'
import { WorkerLabels } from './WorkerLabels'
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
  labels: { rack: 'A', tier: 'gold' },
  status: 'online',
}

function renderLabels(worker: Worker, isAdmin: boolean) {
  setToken('test-token')
  server.use(
    http.get('/v1/users/me', () =>
      HttpResponse.json({ id: 'u1', email: 'a@b.co', name: 'A', is_admin: isAdmin }),
    ),
  )
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <AuthProvider>
          <WorkerLabels worker={worker} />
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => clearToken())

test('renders existing labels as chips', async () => {
  renderLabels(WORKER, false)
  expect(await screen.findByText('rack=A')).toBeInTheDocument()
  expect(screen.getByText('tier=gold')).toBeInTheDocument()
})

test('non-admin sees no remove affordance and no add-label control', async () => {
  renderLabels(WORKER, false)
  await screen.findByText('rack=A')
  expect(screen.queryByRole('button', { name: /remove rack/i })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /add label/i })).not.toBeInTheDocument()
})

test('admin adds a key=value label on Enter, PATCHing the merged map', async () => {
  let body: unknown
  server.use(
    http.patch(`/v1/workers/${ID}`, async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ ...WORKER, labels: { rack: 'A', tier: 'gold', zone: 'east' } })
    }),
  )
  renderLabels(WORKER, true)
  await userEvent.click(await screen.findByRole('button', { name: /add label/i }))
  const input = screen.getByRole('textbox')
  await userEvent.type(input, 'zone=east{Enter}')
  await waitFor(() => expect(body).toEqual({ labels: { rack: 'A', tier: 'gold', zone: 'east' } }))
})

test('admin adds a bare tag (no "=") as a key with empty value', async () => {
  let body: unknown
  server.use(
    http.patch(`/v1/workers/${ID}`, async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ ...WORKER, labels: { rack: 'A', tier: 'gold', urgent: '' } })
    }),
  )
  renderLabels(WORKER, true)
  await userEvent.click(await screen.findByRole('button', { name: /add label/i }))
  const input = screen.getByRole('textbox')
  await userEvent.type(input, 'urgent{Enter}')
  await waitFor(() => expect(body).toEqual({ labels: { rack: 'A', tier: 'gold', urgent: '' } }))
})

test('admin removes a chip, PATCHing the map without that key', async () => {
  let body: unknown
  server.use(
    http.patch(`/v1/workers/${ID}`, async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ ...WORKER, labels: { rack: 'A' } })
    }),
  )
  renderLabels(WORKER, true)
  await userEvent.click(await screen.findByRole('button', { name: /remove tier/i }))
  await waitFor(() => expect(body).toEqual({ labels: { rack: 'A' } }))
})

test('Escape cancels the add-label input without submitting', async () => {
  let hits = 0
  server.use(
    http.patch(`/v1/workers/${ID}`, () => {
      hits++
      return HttpResponse.json(WORKER)
    }),
  )
  renderLabels(WORKER, true)
  await userEvent.click(await screen.findByRole('button', { name: /add label/i }))
  const input = screen.getByRole('textbox')
  await userEvent.type(input, 'zone=east')
  await userEvent.keyboard('{Escape}')
  expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
  expect(hits).toBe(0)
})

test('blur cancels the add-label input without submitting', async () => {
  let hits = 0
  server.use(
    http.patch(`/v1/workers/${ID}`, () => {
      hits++
      return HttpResponse.json(WORKER)
    }),
  )
  renderLabels(WORKER, true)
  await userEvent.click(await screen.findByRole('button', { name: /add label/i }))
  const input = screen.getByRole('textbox')
  await userEvent.type(input, 'zone=east')
  await userEvent.tab()
  expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
  expect(hits).toBe(0)
})

test('empty input on Enter is a no-op', async () => {
  let hits = 0
  server.use(
    http.patch(`/v1/workers/${ID}`, () => {
      hits++
      return HttpResponse.json(WORKER)
    }),
  )
  renderLabels(WORKER, true)
  await userEvent.click(await screen.findByRole('button', { name: /add label/i }))
  const input = screen.getByRole('textbox')
  await userEvent.type(input, '{Enter}')
  expect(hits).toBe(0)
  // Input should stay open, ready for input, on an empty-string no-op.
  expect(screen.getByRole('textbox')).toBeInTheDocument()
})

test('a mutation failure renders an inline error banner', async () => {
  server.use(
    http.patch(`/v1/workers/${ID}`, () => HttpResponse.json({ error: 'boom' }, { status: 500 })),
  )
  renderLabels(WORKER, true)
  await userEvent.click(await screen.findByRole('button', { name: /remove tier/i }))
  expect(await screen.findByText(/boom|500/)).toBeInTheDocument()
})
