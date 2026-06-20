import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { clearToken } from '../lib/token'
import { AuthProvider } from './AuthProvider'
import { LoginScreen } from './LoginScreen'

afterEach(() => clearToken())

function renderLogin() {
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter>
        <AuthProvider>
          <LoginScreen />
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

test('shows a generic message on 401', async () => {
  server.use(
    http.post('/v1/auth/login', () =>
      HttpResponse.json({ error: 'invalid_credentials' }, { status: 401 }),
    ),
  )
  renderLogin()
  await userEvent.type(screen.getByLabelText('Email'), 'ada@studio.dev')
  await userEvent.type(screen.getByLabelText('Password'), 'wrongpw')
  await userEvent.click(screen.getByRole('button', { name: /sign in/i }))
  expect(await screen.findByText(/invalid email or password/i)).toBeInTheDocument()
})

test('shows a rate-limit hint on 429', async () => {
  server.use(
    http.post('/v1/auth/login', () =>
      HttpResponse.json({ error: 'rate_limited' }, { status: 429 }),
    ),
  )
  renderLogin()
  await userEvent.type(screen.getByLabelText('Email'), 'ada@studio.dev')
  await userEvent.type(screen.getByLabelText('Password'), 'pw')
  await userEvent.click(screen.getByRole('button', { name: /sign in/i }))
  expect(await screen.findByText(/too many attempts/i)).toBeInTheDocument()
})
