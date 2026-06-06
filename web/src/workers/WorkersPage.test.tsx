import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { renderWithQuery } from '../test/renderWithQuery'
import { WorkersPage } from './WorkersPage'

afterEach(() => localStorage.clear())

const page = {
  items: [
    { id: 'w1', name: 'render-01', hostname: 'h', cpu_cores: 16, ram_gb: 128, gpu_count: 1, gpu_model: 'RTX 4090', os: 'linux', max_slots: 4, labels: null, status: 'online', last_seen_at: '2026-06-03T12:00:00Z' },
    { id: 'w2', name: 'render-02', hostname: 'h', cpu_cores: 8, ram_gb: 64, gpu_count: 0, gpu_model: '', os: 'linux', max_slots: 2, labels: null, status: 'offline' },
  ],
  next_cursor: '',
  total: 2,
}

const stats = { online: 1, stale: 0, offline: 1, disabled: 0, total: 2 }

beforeEach(() => {
  server.use(http.get('/v1/workers/stats', () => HttpResponse.json(stats)))
})

test('renders workers and the fleet-wide summary', async () => {
  server.use(http.get('/v1/workers', () => HttpResponse.json(page)))
  renderWithQuery(<WorkersPage />)
  expect(await screen.findByText('render-01')).toBeInTheDocument()
  expect(screen.getByText('render-02')).toBeInTheDocument()
  expect(screen.getByText('2 workers')).toBeInTheDocument()
})

test('view toggle switches to the table and persists to localStorage', async () => {
  server.use(http.get('/v1/workers', () => HttpResponse.json(page)))
  renderWithQuery(<WorkersPage />)
  await screen.findByText('render-01')
  await userEvent.click(screen.getByRole('button', { name: 'Table' }))
  expect(screen.getByRole('button', { name: /name/i })).toBeInTheDocument()
  expect(localStorage.getItem('relay.workers.view')).toBe('table')
})

test('view toggle reports aria-pressed on the active button', async () => {
  server.use(http.get('/v1/workers', () => HttpResponse.json(page)))
  renderWithQuery(<WorkersPage />)
  await screen.findByText('render-01')
  expect(screen.getByRole('button', { name: 'Grid' })).toHaveAttribute('aria-pressed', 'true')
  expect(screen.getByRole('button', { name: 'Table' })).toHaveAttribute('aria-pressed', 'false')
  await userEvent.click(screen.getByRole('button', { name: 'Table' }))
  expect(screen.getByRole('button', { name: 'Table' })).toHaveAttribute('aria-pressed', 'true')
  expect(screen.getByRole('button', { name: 'Grid' })).toHaveAttribute('aria-pressed', 'false')
})

test('clicking a sort header re-requests with the new sort', async () => {
  const sorts: (string | null)[] = []
  server.use(
    http.get('/v1/workers', ({ request }) => {
      sorts.push(new URL(request.url).searchParams.get('sort'))
      return HttpResponse.json(page)
    }),
  )
  renderWithQuery(<WorkersPage />)
  await screen.findByText('render-01')
  await userEvent.click(screen.getByRole('button', { name: 'Table' }))
  await userEvent.click(screen.getByRole('button', { name: /name/i }))
  await waitFor(() => expect(sorts).toContain('name'))
})

test('shows an error banner with retry, then recovers', async () => {
  server.use(http.get('/v1/workers', () => HttpResponse.json({ error: 'boom' }, { status: 500 })))
  renderWithQuery(<WorkersPage />)
  expect(await screen.findByRole('button', { name: /retry/i })).toBeInTheDocument()

  server.use(http.get('/v1/workers', () => HttpResponse.json(page)))
  await userEvent.click(screen.getByRole('button', { name: /retry/i }))
  expect(await screen.findByText('render-01')).toBeInTheDocument()
})

test('shows the empty state when there are no workers', async () => {
  server.use(http.get('/v1/workers', () => HttpResponse.json({ items: [], next_cursor: '', total: 0 })))
  renderWithQuery(<WorkersPage />)
  expect(await screen.findByText(/no workers enrolled yet/i)).toBeInTheDocument()
})

test('summary strip shows fleet-wide totals from the stats endpoint', async () => {
  server.use(http.get('/v1/workers', () => HttpResponse.json(page))) // page total = 2
  server.use(
    http.get('/v1/workers/stats', () =>
      HttpResponse.json({ online: 4, stale: 0, offline: 1, disabled: 0, total: 5 }),
    ),
  )
  renderWithQuery(<WorkersPage />)
  // page.total is 2, but the strip must show the fleet-wide total of 5.
  expect(await screen.findByText('5 workers')).toBeInTheDocument()
})
