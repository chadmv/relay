import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { clearToken, getToken, setToken } from '../lib/token'
import { AuthProvider, useAuth } from './AuthProvider'

afterEach(() => clearToken())

const ME = { id: '1', email: 'ada@studio.dev', name: 'Ada', is_admin: false }

function Probe() {
  const { status, user, login, logout } = useAuth()
  return (
    <div>
      <span data-testid="status">{status}</span>
      <span data-testid="user">{user?.email ?? 'none'}</span>
      <button onClick={() => login('ada@studio.dev', 'pw')}>login</button>
      <button onClick={() => logout()}>logout</button>
    </div>
  )
}

function renderProbe() {
  return render(
    <AuthProvider>
      <Probe />
    </AuthProvider>,
  )
}

test('starts unauthenticated with no token', async () => {
  renderProbe()
  await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('anonymous'))
  expect(screen.getByTestId('user')).toHaveTextContent('none')
})

test('hydrates the user from an existing token', async () => {
  server.use(http.get('/v1/users/me', () => HttpResponse.json(ME)))
  setToken('tok_existing')
  renderProbe()
  await waitFor(() => expect(screen.getByTestId('user')).toHaveTextContent('ada@studio.dev'))
  expect(screen.getByTestId('status')).toHaveTextContent('authenticated')
})

test('login stores the token and sets the user', async () => {
  server.use(
    http.post('/v1/auth/login', () =>
      HttpResponse.json({ token: 'tok_new', expires_at: '' }),
    ),
    http.get('/v1/users/me', () => HttpResponse.json(ME)),
  )
  renderProbe()
  await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('anonymous'))
  await userEvent.click(screen.getByText('login'))
  await waitFor(() => expect(screen.getByTestId('user')).toHaveTextContent('ada@studio.dev'))
  expect(getToken()).toBe('tok_new')
})

test('logout clears token and user', async () => {
  server.use(
    http.get('/v1/users/me', () => HttpResponse.json(ME)),
    http.delete('/v1/auth/token', () => new HttpResponse(null, { status: 204 })),
  )
  setToken('tok_existing')
  renderProbe()
  await waitFor(() => expect(screen.getByTestId('user')).toHaveTextContent('ada@studio.dev'))
  await userEvent.click(screen.getByText('logout'))
  await waitFor(() => expect(getToken()).toBeNull())
  expect(screen.getByTestId('user')).toHaveTextContent('none')
})
