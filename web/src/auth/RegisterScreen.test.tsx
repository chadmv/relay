import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { clearToken } from '../lib/token'
import { AuthProvider } from './AuthProvider'
import { RegisterScreen } from './RegisterScreen'

afterEach(() => clearToken())

function renderRegister() {
  return render(
    <MemoryRouter>
      <AuthProvider>
        <RegisterScreen />
      </AuthProvider>
    </MemoryRouter>,
  )
}

test('hides the invite field when self-register is enabled', async () => {
  server.use(http.get('/v1/config', () => HttpResponse.json({ allow_self_register: true })))
  renderRegister()
  await waitFor(() => expect(screen.getByLabelText('Email')).toBeInTheDocument())
  expect(screen.queryByLabelText(/invite token/i)).not.toBeInTheDocument()
})

test('shows the invite field when self-register is disabled', async () => {
  server.use(http.get('/v1/config', () => HttpResponse.json({ allow_self_register: false })))
  renderRegister()
  expect(await screen.findByLabelText(/invite token/i)).toBeInTheDocument()
})

test('shows an inline invite error on 400', async () => {
  server.use(
    http.get('/v1/config', () => HttpResponse.json({ allow_self_register: false })),
    http.post('/v1/auth/register', () =>
      HttpResponse.json({ error: 'invite_expired' }, { status: 400 }),
    ),
  )
  renderRegister()
  await userEvent.type(await screen.findByLabelText('Display name'), 'Ada')
  await userEvent.type(screen.getByLabelText('Email'), 'ada@studio.dev')
  await userEvent.type(screen.getByLabelText(/invite token/i), 'rl_invt_x')
  await userEvent.type(screen.getByLabelText('Password'), 'password1')
  await userEvent.click(screen.getByRole('button', { name: /create account/i }))
  expect(await screen.findByText(/invite_expired/i)).toBeInTheDocument()
})

test('shows email-exists error with sign-in link on 409', async () => {
  server.use(
    http.get('/v1/config', () => HttpResponse.json({ allow_self_register: true })),
    http.post('/v1/auth/register', () =>
      HttpResponse.json({ error: 'email_taken' }, { status: 409 }),
    ),
  )
  renderRegister()
  await userEvent.type(await screen.findByLabelText('Display name'), 'Ada')
  await userEvent.type(screen.getByLabelText('Email'), 'ada@studio.dev')
  await userEvent.type(screen.getByLabelText('Password'), 'password1')
  await userEvent.click(screen.getByRole('button', { name: /create account/i }))
  expect(await screen.findByText(/already registered/i)).toBeInTheDocument()
  expect(screen.getByRole('link', { name: /sign in/i })).toBeInTheDocument()
})
