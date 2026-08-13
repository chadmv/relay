import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { AuthProvider } from '../auth/AuthProvider'
import { clearToken, setToken } from '../lib/token'
import { ProfilePage } from './ProfilePage'

afterEach(() => clearToken())

const ME = {
  id: 'u1',
  email: 'mira@studio.dev',
  name: 'Mira Sato',
  is_admin: true,
  created_at: '2025-04-02T09:15:00Z',
  archived_at: null,
}

function renderAt(path: string, me: Record<string, unknown> = ME) {
  setToken('tok')
  server.use(http.get('/v1/users/me', () => HttpResponse.json(me)))
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <AuthProvider>
          <Routes>
            <Route path="/profile/:tab" element={<ProfilePage />} />
          </Routes>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

test('renders the eyebrow, the initials avatar, the name heading and the tab bar', async () => {
  renderAt('/profile/identity')
  expect(await screen.findByRole('heading', { level: 1, name: /Mira Sato/ })).toBeInTheDocument()
  expect(screen.getByText('YOUR ACCOUNT')).toBeInTheDocument()
  expect(screen.getByTestId('profile-initials')).toHaveTextContent('MS')
  expect(screen.getByRole('link', { name: 'Identity' })).toHaveAttribute(
    'aria-current',
    'page',
  )
})

test('the meta strip shows EMAIL, ROLE and MEMBER SINCE with real values', async () => {
  renderAt('/profile/identity')
  await screen.findByRole('heading', { level: 1, name: /Mira Sato/ })
  expect(screen.getByText('EMAIL')).toBeInTheDocument()
  expect(screen.getByTestId('meta-email')).toHaveTextContent('mira@studio.dev')
  expect(screen.getByTestId('meta-role')).toHaveTextContent('ADMIN')
  // Assert the VALUE, not just the label. created_at is a real runtime dependency
  // on every fixture: type-checking cannot catch a fixture missing it (every
  // fixture is an untyped HttpResponse.json literal), and the house rendering is
  // a string slice (UsersTable.tsx:123), so a missing field throws rather than
  // silently rendering Invalid Date. Either way, only a value assertion catches it.
  expect(screen.getByTestId('meta-member-since')).toHaveTextContent('2025-04-02')
})

test('the meta strip shows USER for a non-admin (paired control on ROLE)', async () => {
  renderAt('/profile/identity', { ...ME, is_admin: false })
  await screen.findByRole('heading', { level: 1, name: /Mira Sato/ })
  expect(screen.getByTestId('meta-role')).toHaveTextContent('USER')
})

test('renders NO unbacked activity facts', async () => {
  renderAt('/profile/identity')
  await screen.findByRole('heading', { level: 1, name: /Mira Sato/ })
  // No column, no endpoint, no proxy: the users table has no last-login field and
  // api_tokens.created_at is issuance, not login, and is unreadable anyway
  // without GET /v1/auth/tokens. Rendering "-" for three of four rows is the
  // VERSION/BUILD strip mistake (AdminPage.tsx:6-14).
  for (const label of ['LAST LOGIN', 'LOGIN COUNT', 'ACTIVE SESSIONS', 'Activity']) {
    expect(screen.queryByText(label)).not.toBeInTheDocument()
  }
})

test('initials handle a one-word name', async () => {
  renderAt('/profile/identity', { ...ME, name: 'Ada' })
  await screen.findByRole('heading', { level: 1, name: /Ada/ })
  expect(screen.getByTestId('profile-initials')).toHaveTextContent('A')
})

test('initials collapse extra whitespace, including a leading/trailing run', async () => {
  // Exercises the split-on-\s+ path that a bare .filter(Boolean) removal could
  // regress: leading/trailing whitespace produces empty parts from split(),
  // and those must still contribute nothing to the two-letter result.
  renderAt('/profile/identity', { ...ME, name: '  Mira   Sato  ' })
  await screen.findByRole('heading', { level: 1 })
  expect(screen.getByTestId('profile-initials')).toHaveTextContent('MS')
})

test('an unknown tab segment redirects to identity', async () => {
  render(
    (() => {
      setToken('tok')
      server.use(http.get('/v1/users/me', () => HttpResponse.json(ME)))
      const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
      return (
        <QueryClientProvider client={client}>
          <MemoryRouter initialEntries={['/profile/nope']}>
            <AuthProvider>
              <Routes>
                <Route path="/profile/:tab" element={<ProfilePage />} />
              </Routes>
            </AuthProvider>
          </MemoryRouter>
        </QueryClientProvider>
      )
    })(),
  )
  // The redirect lands on /profile/identity, which the same route renders.
  expect(await screen.findByLabelText('Display name')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Identity' })).toHaveAttribute('aria-current', 'page')
})

test('each tab route renders its own panel and not the others', async () => {
  renderAt('/profile/password')
  expect(await screen.findByLabelText('Current password')).toBeInTheDocument()
  expect(screen.queryByLabelText('Display name')).toBeNull()
  expect(screen.queryByRole('button', { name: 'Sign out everywhere' })).toBeNull()
})

test('the sessions route renders the sessions panel', async () => {
  renderAt('/profile/sessions')
  expect(await screen.findByRole('button', { name: 'Sign out everywhere' })).toBeInTheDocument()
  expect(screen.queryByLabelText('Display name')).toBeNull()
})

test('a successful rename updates the HEADING, not just the input', async () => {
  server.use(http.patch('/v1/users/me', () => HttpResponse.json({ ...ME, name: 'Mira Renamed' })))
  renderAt('/profile/identity')
  const input = await screen.findByLabelText('Display name')
  await userEvent.clear(input)
  await userEvent.type(input, 'Mira Renamed')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))

  // The header is fed from AuthProvider, so this is the end-to-end proof that
  // applyUser ran. A component that only set local form state passes an
  // input-reading test and leaves the header stale for the rest of the session.
  await waitFor(() =>
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Mira Renamed'),
  )
  // The initials follow the same source.
  expect(screen.getByTestId('profile-initials')).toHaveTextContent('MR')
})
