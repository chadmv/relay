import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { AuthProvider } from '../auth/AuthProvider'
import { clearToken, setToken } from '../lib/token'
import { HoloShell } from './HoloShell'

function renderShell(isAdmin: boolean) {
  setToken('tok')
  server.use(
    http.get('/v1/users/me', () =>
      HttpResponse.json({ id: 'me', email: 'me@studio.dev', name: 'Me', is_admin: isAdmin }),
    ),
  )
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/jobs']}>
        <AuthProvider>
          <HoloShell>
            <div>page body</div>
          </HoloShell>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => clearToken())

test('always shows the non-admin nav entries', async () => {
  renderShell(false)
  await waitFor(() => expect(screen.getByText('page body')).toBeInTheDocument())
  for (const label of ['Jobs', 'Workers', 'Schedules']) {
    expect(screen.getByRole('link', { name: label })).toBeInTheDocument()
  }
})

test('hides the Admin nav entry from non-admins', async () => {
  renderShell(false)
  await waitFor(() => expect(screen.getByRole('link', { name: 'Jobs' })).toBeInTheDocument())
  expect(screen.queryByRole('link', { name: 'Admin' })).not.toBeInTheDocument()
})

test('shows the Admin nav entry to admins', async () => {
  renderShell(true)
  expect(await screen.findByRole('link', { name: 'Admin' })).toHaveAttribute('href', '/admin')
})
