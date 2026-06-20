import { render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { AuthProvider } from '../auth/AuthProvider'
import { clearToken, setToken } from '../lib/token'
import { ProtectedRoute } from './ProtectedRoute'

afterEach(() => clearToken())

function renderAt(path: string) {
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter initialEntries={[path]}>
        <AuthProvider>
          <Routes>
            <Route path="/auth" element={<div>login page</div>} />
            <Route element={<ProtectedRoute />}>
              <Route path="/jobs" element={<div>jobs page</div>} />
            </Route>
          </Routes>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

test('redirects anonymous users to /auth', async () => {
  renderAt('/jobs')
  await waitFor(() => expect(screen.getByText('login page')).toBeInTheDocument())
})

test('renders the protected route when authenticated', async () => {
  server.use(
    http.get('/v1/users/me', () =>
      HttpResponse.json({ id: '1', email: 'a@b.co', name: 'A', is_admin: false }),
    ),
  )
  setToken('tok')
  renderAt('/jobs')
  await waitFor(() => expect(screen.getByText('jobs page')).toBeInTheDocument())
})
