import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { clearToken } from '../lib/token'
import { AuthProvider } from './AuthProvider'
import { RegisterScreen } from './RegisterScreen'

afterEach(() => clearToken())

function renderRegister() {
  return render(
    // retry: false. The added test answers /config with a 500, and a bare
    // QueryClient retries three times with backoff, which would make it slow and
    // timing-sensitive. Inert for the tests that answer /config successfully.
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MemoryRouter>
        <AuthProvider>
          <RegisterScreen />
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
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

// The register form renders on a LATER commit than the component - the /config
// early return holds until that request resolves - so this test is what
// discriminates the autoFocus attribute from a []-deps mount effect, which would
// run when there is no node to focus and never run again.
test('the display name field takes focus once the register form renders', async () => {
  server.use(http.get('/v1/config', () => HttpResponse.json({ allow_self_register: true })))
  renderRegister()
  const name = await screen.findByLabelText('Display name')
  // Positive control: the form rendered, not just the empty placeholder.
  expect(
    screen.getByRole('heading', { name: 'Create your relay account', level: 1 }),
  ).toBeInTheDocument()
  expect(document.activeElement).toBe(name)
})

// The fail-closed policy: a /config failure must show the INVITE field, because
// the server enforces the invite requirement regardless, so a wrong client hint
// is a nuisance and never a bypass. Discriminating because the opposite policy
// (deriving true on error) and a query that never leaves pending both render a
// page with no invite field on exactly this input.
test('a failed config fetch shows the invite field', async () => {
  server.use(
    http.get('/v1/config', () => HttpResponse.json({ error: 'boom' }, { status: 500 })),
  )
  renderRegister()
  expect(await screen.findByLabelText(/invite token/i)).toBeInTheDocument()
})

// Mutation-proven gap: `config.data?.allow_self_register ?? null` mutated to
// `?? true` survives on the invite-token assertion alone, because a true guess
// ALSO hides the invite field - just for the wrong reason (self-register looks
// enabled) rather than the right one (nothing has rendered yet). The heading is
// the real discriminator: the correct code renders the blank placeholder while
// /config is in flight, so neither the form nor its invite field exists; the
// mutant renders the full form immediately, guessing open registration.
test('a pending config fetch renders neither the form nor a premature guess', async () => {
  server.use(http.get('/v1/config', () => new Promise(() => {})))
  renderRegister()
  expect(screen.queryByRole('heading', { name: 'Create your relay account' })).not.toBeInTheDocument()
  expect(screen.queryByLabelText(/invite token/i)).not.toBeInTheDocument()
})
