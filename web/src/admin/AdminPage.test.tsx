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
