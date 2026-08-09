import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { AuthProvider } from '../auth/AuthProvider'
import { clearToken, setToken } from '../lib/token'
import { AppRoutes } from './router'

const USERS_PAGE = {
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
}

// Exercises the REAL router tree, so a guard that works in isolation but is wired
// wrong in router.tsx still fails here.
function renderApp(path: string, isAdmin: boolean) {
  setToken('tok')
  server.use(
    http.get('/v1/users/me', () =>
      HttpResponse.json({ id: 'me', email: 'me@studio.dev', name: 'Me', is_admin: isAdmin }),
    ),
    http.get('/v1/users', () => HttpResponse.json(USERS_PAGE)),
    // Only hit on the non-admin redirect to /jobs.
    http.get('/v1/jobs', () => HttpResponse.json({ items: [], next_cursor: '', total: 0 })),
    http.get('/v1/jobs/stats', () =>
      HttpResponse.json({ running: 0, queued: 0, done_24h: 0, failed_24h: 0 }),
    ),
  )
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

test('/admin redirects an admin to the users tab', async () => {
  renderApp('/admin', true)
  expect(await screen.findByRole('heading', { level: 1, name: 'Admin' })).toBeInTheDocument()
  expect(await screen.findByText('ada@studio.dev')).toBeInTheDocument()
})

test('/admin/bogus redirects an admin to the users tab', async () => {
  renderApp('/admin/bogus', true)
  expect(await screen.findByText('ada@studio.dev')).toBeInTheDocument()
})

test('a non-admin at /admin/users is redirected to /jobs', async () => {
  renderApp('/admin/users', false)
  expect(await screen.findByRole('heading', { level: 1, name: 'Jobs' })).toBeInTheDocument()
  expect(screen.queryByRole('heading', { level: 1, name: 'Admin' })).not.toBeInTheDocument()
})

test('a non-admin at /admin is redirected to /jobs', async () => {
  renderApp('/admin', false)
  expect(await screen.findByRole('heading', { level: 1, name: 'Jobs' })).toBeInTheDocument()
})

test('a non-admin sees no Admin nav entry and an admin does', async () => {
  const nonAdmin = renderApp('/jobs', false)
  await waitFor(() => expect(screen.getByRole('heading', { level: 1, name: 'Jobs' })).toBeInTheDocument())
  expect(screen.queryByRole('link', { name: 'Admin' })).not.toBeInTheDocument()
  nonAdmin.unmount()
  clearToken()

  renderApp('/jobs', true)
  expect(await screen.findByRole('link', { name: 'Admin' })).toBeInTheDocument()
})
