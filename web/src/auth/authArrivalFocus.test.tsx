import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { apiFetch } from '../lib/api'
import { clearToken, setToken } from '../lib/token'
import { ProtectedRoute } from '../app/ProtectedRoute'
import { AuthProvider } from './AuthProvider'
import { LoginScreen } from './LoginScreen'

afterEach(() => clearToken())

const ME = { id: '1', email: 'ada@studio.dev', name: 'Ada', is_admin: false }

// Render shape from app/ProtectedRoute.test.tsx. The protected element is a bare
// div: ProtectedRoute still renders HoloShell and the real UserMenu around it,
// which is the departure point these tests need, while the page itself issues no
// request the lane would have to stub.
function renderAt(path: string) {
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter initialEntries={[path]}>
        <AuthProvider>
          <Routes>
            <Route path="/auth" element={<LoginScreen />} />
            <Route element={<ProtectedRoute />}>
              <Route path="/jobs" element={<div>jobs page</div>} />
            </Route>
          </Routes>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

// findByRole is both the positive control that the sign-in page really rendered
// and the await that keeps the arriving commit inside act.
async function expectFocusOnEmail() {
  await screen.findByRole('heading', { name: 'Sign in', level: 1 })
  expect(document.activeElement).toBe(screen.getByLabelText('Email'))
}

test('arriving at /auth unauthenticated puts focus on the email field', async () => {
  renderAt('/auth')
  await expectFocusOnEmail()
})

test('signing out from the account menu leaves focus on the email field, not on <body>', async () => {
  server.use(
    http.get('/v1/users/me', () => HttpResponse.json(ME)),
    http.delete('/v1/auth/token', () => new HttpResponse(null, { status: 204 })),
  )
  setToken('tok')
  renderAt('/jobs')
  // Drive the real component: HoloShell renders UserMenu, whose toggle's
  // accessible name is the signed-in email. A synthetic logout button would not
  // exercise the path this test is about.
  await userEvent.click(await screen.findByRole('button', { name: /ada@studio.dev/i }))
  await userEvent.click(screen.getByText('Log out'))

  await expectFocusOnEmail()
})

test('a 401 teardown lands on the sign-in page with focus on the email field', async () => {
  server.use(
    http.get('/v1/users/me', () => HttpResponse.json(ME)),
    http.get('/v1/jobs/stats', () =>
      HttpResponse.json({ error: 'unauthorized' }, { status: 401 }),
    ),
  )
  setToken('tok')
  renderAt('/jobs')
  await screen.findByText('jobs page')

  // The real teardown, not a hand-rolled navigation: apiFetch stamps the 401 with
  // the token the request carried, AuthProvider's subscription clears the session,
  // and ProtectedRoute redirects. Precedent: authTokenSecrecy.test.tsx.
  await apiFetch('/jobs/stats').catch(() => {})

  await expectFocusOnEmail()
})
