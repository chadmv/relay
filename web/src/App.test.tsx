import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test } from 'vitest'
import { server } from './test/setup-helpers'
import { clearToken } from './lib/token'
import { App } from './App'

afterEach(() => clearToken())

test('anonymous user landing on / is sent to the sign-in screen', async () => {
  render(<App />)
  await waitFor(() =>
    expect(screen.getByText(/sign in to the coordinator/i)).toBeInTheDocument(),
  )
})

test('a successful login lands the user on the jobs page', async () => {
  server.use(
    http.post('/v1/auth/login', () =>
      HttpResponse.json({ token: 'tok_new', expires_at: '' }),
    ),
    http.get('/v1/users/me', () =>
      HttpResponse.json({ id: '1', email: 'admin@example.com', name: 'Admin', is_admin: true }),
    ),
  )
  render(<App />)
  await waitFor(() =>
    expect(screen.getByText(/sign in to the coordinator/i)).toBeInTheDocument(),
  )
  await userEvent.type(screen.getByLabelText('Email'), 'admin@example.com')
  await userEvent.type(screen.getByLabelText('Password'), 'changeme123')
  await userEvent.click(screen.getByRole('button', { name: /sign in/i }))
  expect(await screen.findByText(/coming soon/i)).toBeInTheDocument()
})
