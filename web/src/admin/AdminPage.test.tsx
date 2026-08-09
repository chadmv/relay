import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { MemoryRouter, Route, Routes, useParams } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { AuthProvider } from '../auth/AuthProvider'
import { clearToken, setToken } from '../lib/token'
import { AdminPage } from './AdminPage'

const ME = { id: 'me', email: 'me@studio.dev', name: 'Me', is_admin: true }

function Landing() {
  const { tab } = useParams()
  return <div>landed on {tab}</div>
}

function renderAt(path: string) {
  setToken('tok')
  server.use(
    http.get('/v1/users/me', () => HttpResponse.json(ME)),
    http.get('/v1/users', () =>
      HttpResponse.json({
        items: [
          {
            id: 'u1',
            email: 'ada@studio.dev',
            name: 'Ada',
            is_admin: false,
            created_at: '2026-08-01T12:00:00Z',
            archived_at: null,
          },
        ],
        next_cursor: '',
        total: 1,
      }),
    ),
    http.get('/v1/agent-enrollments', () =>
      HttpResponse.json({
        items: [
          {
            id: 'e1',
            created_at: '2026-08-09T09:30:00Z',
            expires_at: '2026-08-10T09:42:00Z',
            created_by: 'u1',
            hostname_hint: 'farm-west-13',
          },
        ],
        next_cursor: '',
        total: 1,
      }),
    ),
    http.get('/v1/reservations', () =>
      HttpResponse.json({
        items: [
          {
            id: 'r1',
            name: 'gpu-farm-hold',
            selector: null,
            worker_ids: ['aaaa1111-1111-1111-1111-111111111111'],
            user_id: 'u1',
            created_at: '2026-08-09T09:30:00Z',
          },
        ],
        next_cursor: '',
        total: 1,
      }),
    ),
    http.get('/v1/jobs/stats', () =>
      HttpResponse.json({ running: 11, queued: 22, done_24h: 33, failed_24h: 44 }),
    ),
    http.get('/v1/workers/stats', () =>
      HttpResponse.json({ online: 55, stale: 66, offline: 77, disabled: 88, total: 99 }),
    ),
    http.get('/v1/config', () => HttpResponse.json({ allow_self_register: false })),
    http.get('/v1/health', () => HttpResponse.json({ status: 'ok' })),
  )
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <AuthProvider>
          <Routes>
            <Route path="/admin/users" element={<AdminPage />} />
            <Route path="/admin/:tab" element={<AdminPage />} />
            <Route path="/landed/:tab" element={<Landing />} />
          </Routes>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => clearToken())

test('renders the hi-fi header, the tab bar, and the Users panel', async () => {
  renderAt('/admin/users')
  expect(screen.getByText('SETTINGS · ADMIN ONLY')).toBeInTheDocument()
  expect(screen.getByRole('heading', { level: 1, name: 'Admin' })).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Users' })).toBeInTheDocument()
  expect(await screen.findByText('ada@studio.dev')).toBeInTheDocument()
})

test('renders no unbacked server-facts strip', () => {
  renderAt('/admin/users')
  for (const label of ['VERSION', 'BUILD', 'DB', 'UPTIME']) {
    expect(screen.queryByText(label)).not.toBeInTheDocument()
  }
})

test('an unknown tab segment redirects to the users tab', async () => {
  renderAt('/admin/bogus')
  // The redirect lands on the /admin/users route, which renders the shell + panel.
  expect(await screen.findByText('ada@studio.dev')).toBeInTheDocument()
  expect(screen.getByRole('heading', { level: 1, name: 'Admin' })).toBeInTheDocument()
})

test('a not-yet-built tab segment redirects rather than rendering an empty shell', async () => {
  renderAt('/admin/invites')
  expect(await screen.findByText('ada@studio.dev')).toBeInTheDocument()
})

test('/admin/enrollments renders the enrollments panel inside the same shell', async () => {
  renderAt('/admin/enrollments')
  expect(screen.getByText('SETTINGS · ADMIN ONLY')).toBeInTheDocument()
  expect(screen.getByRole('heading', { level: 1, name: 'Admin' })).toBeInTheDocument()
  expect(await screen.findByText('farm-west-13')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Agent enrolls' })).toHaveAttribute('aria-current', 'page')
})

test('/admin/users still renders the Users panel', async () => {
  renderAt('/admin/users')
  expect(await screen.findByText('ada@studio.dev')).toBeInTheDocument()
  expect(screen.queryByText('farm-west-13')).not.toBeInTheDocument()
})

test('/admin/reservations renders the reservations panel inside the same shell', async () => {
  renderAt('/admin/reservations')
  expect(screen.getByText('SETTINGS · ADMIN ONLY')).toBeInTheDocument()
  expect(screen.getByRole('heading', { level: 1, name: 'Admin' })).toBeInTheDocument()
  expect(await screen.findByText('gpu-farm-hold')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Reservations' })).toHaveAttribute(
    'aria-current',
    'page',
  )
})

test('/admin/users still renders its own panel, not reservations', async () => {
  renderAt('/admin/users')
  expect(await screen.findByText('ada@studio.dev')).toBeInTheDocument()
  expect(screen.queryByText('gpu-farm-hold')).not.toBeInTheDocument()
})

// L8 (review 2026-08-09): the previous test's name promised both /admin/users AND
// /admin/enrollments but only ever rendered /admin/users - it duplicated the check
// at line ~123 above and would still pass if /admin/enrollments regressed to
// rendering the Reservations panel instead of its own.
test('/admin/enrollments still renders its own panel, not reservations', async () => {
  renderAt('/admin/enrollments')
  expect(await screen.findByText('farm-west-13')).toBeInTheDocument()
  expect(screen.queryByText('gpu-farm-hold')).not.toBeInTheDocument()
})

test('/admin/server renders the server panel inside the same shell and marks the pill active', async () => {
  renderAt('/admin/server')
  expect(screen.getByText('SETTINGS · ADMIN ONLY')).toBeInTheDocument()
  expect(screen.getByRole('heading', { level: 1, name: 'Admin' })).toBeInTheDocument()
  expect(await screen.findByText('Server overview')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Server' })).toHaveAttribute('aria-current', 'page')
})

test('/admin/users still renders its own panel, not the server overview', async () => {
  renderAt('/admin/users')
  expect(await screen.findByText('ada@studio.dev')).toBeInTheDocument()
  expect(screen.queryByText('Server overview')).not.toBeInTheDocument()
})
