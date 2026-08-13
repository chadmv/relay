import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { AuthProvider } from '../auth/AuthProvider'
import { clearToken, setToken } from '../lib/token'
import { AppRoutes } from '../app/router'

afterEach(() => clearToken())

const ME = {
  id: 'u1',
  email: 'mira@studio.dev',
  name: 'Mira Sato',
  is_admin: false,
  created_at: '2025-04-02T09:15:00Z',
  archived_at: null,
}

function renderApp(path: string) {
  setToken('tok')
  server.use(http.get('/v1/users/me', () => HttpResponse.json(ME)))
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
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

test('/profile - the UserMenu Profile link - lands on the Identity tab', async () => {
  // UserMenu.tsx:60 links here and is NOT modified by this slice.
  renderApp('/profile')
  expect(await screen.findByLabelText('Display name')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Identity' })).toHaveAttribute('aria-current', 'page')
})

test('/profile/password - the UserMenu Password link - lands on the Password tab', async () => {
  // UserMenu.tsx:64.
  renderApp('/profile/password')
  expect(await screen.findByLabelText('Current password')).toBeInTheDocument()
})

test('/profile/sessions - the UserMenu Sessions link - lands on the Sessions tab', async () => {
  // UserMenu.tsx:70.
  renderApp('/profile/sessions')
  expect(await screen.findByRole('button', { name: 'Sign out everywhere' })).toBeInTheDocument()
})

test('an unknown /profile/:tab redirects to identity', async () => {
  renderApp('/profile/nope')
  expect(await screen.findByLabelText('Display name')).toBeInTheDocument()
})

test('the placeholder is unreachable from every /profile route', async () => {
  // The exact text JobsPlaceholder rendered ("Jobs - coming soon."). This is the
  // acceptance criterion that the three dead links are actually dead no longer;
  // asserted on all four routes so a partial wiring cannot pass.
  for (const path of ['/profile', '/profile/identity', '/profile/password', '/profile/sessions']) {
    const { unmount } = renderApp(path)
    expect(await screen.findByText('YOUR ACCOUNT')).toBeInTheDocument()
    expect(screen.queryByText(/coming soon/i)).toBeNull()
    unmount()
  }
})

test('the profile routes are NOT admin-gated - a non-admin reaches all three', async () => {
  // Every endpoint behind this page is auth(...), never AdminOnly
  // (internal/api/server.go:97-100, :153). Putting these routes behind AdminRoute
  // would lock out exactly the users who need them, and the ME fixture above is
  // deliberately is_admin: false so every test in this file is that assertion.
  renderApp('/profile/sessions')
  expect(await screen.findByRole('button', { name: 'Sign out everywhere' })).toBeInTheDocument()
  expect(screen.getByTestId('meta-role')).toHaveTextContent('USER')
})
