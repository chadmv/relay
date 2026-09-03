import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { clearToken } from '../lib/token'
import { AuthProvider, useAuth } from './AuthProvider'

// Hand-written literals carrying every key the server sends, including
// archived_at, which userResponse emits with no omitempty and which
// web/src/lib/types.ts deliberately does not model. Not built from LoginResponse
// or User: a fixture marshalled through the decoder's own type agrees with it by
// construction and can never detect drift in either direction.
const BODY_USER = {
  id: 'u-body',
  email: 'body@studio.dev',
  name: 'Body User',
  is_admin: false,
  created_at: '2026-01-02T03:04:05Z',
  archived_at: null,
}

const ENDPOINT_USER = {
  id: 'u-endpoint',
  email: 'endpoint@studio.dev',
  name: 'Endpoint User',
  is_admin: false,
  created_at: '2026-02-03T04:05:06Z',
  archived_at: null,
}

// Counted rather than inferred from MSW's unhandled-request error: a count names
// the route, and "this route was never called" is the whole assertion here.
let meCalls = 0

function withMe() {
  server.use(
    http.get('/v1/users/me', () => {
      meCalls++
      return HttpResponse.json(ENDPOINT_USER)
    }),
  )
}

function Probe() {
  const { status, user, login, register } = useAuth()
  return (
    <div>
      <span data-testid="status">{status}</span>
      <span data-testid="email">{user?.email ?? 'none'}</span>
      <span data-testid="created">{user?.created_at ?? 'none'}</span>
      <button onClick={() => login('a@b.co', 'pw')}>login</button>
      <button onClick={() => register({ email: 'a@b.co', name: 'A', password: 'password1' })}>
        register
      </button>
    </div>
  )
}

function renderProbe() {
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <AuthProvider>
        <Probe />
      </AuthProvider>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  meCalls = 0
})

afterEach(() => clearToken())

// Kills: keeping the unconditional round trip. The zero-request assertion is the
// discriminator - a client that reads res.user AND still fetches /users/me would
// render the right email and leave meCalls at 1.
test('login uses the user object in the body', async () => {
  withMe()
  server.use(
    http.post('/v1/auth/login', () =>
      HttpResponse.json({ token: 'tok_1', expires_at: '', user: BODY_USER }),
    ),
  )
  renderProbe()
  await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('anonymous'))
  await userEvent.click(screen.getByText('login'))
  await waitFor(() => expect(screen.getByTestId('email')).toHaveTextContent('body@studio.dev'))
  expect(screen.getByTestId('status')).toHaveTextContent('authenticated')
  await new Promise((r) => setTimeout(r, 20))
  expect(meCalls).toBe(0)
})

// Kills: dropping the fallback. A body with no user key is what an older server
// sends, and a client with no fallback commits an undefined row into a session it
// has already marked authenticated.
test('an older server without a user object still signs in', async () => {
  withMe()
  server.use(
    http.post('/v1/auth/login', () => HttpResponse.json({ token: 'tok_2', expires_at: '' })),
  )
  renderProbe()
  await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('anonymous'))
  await userEvent.click(screen.getByText('login'))
  await waitFor(() => expect(screen.getByTestId('email')).toHaveTextContent('endpoint@studio.dev'))
  expect(meCalls).toBe(1)
})

// Kills: guarding on the PRESENCE of `user` alone. created_at is what the profile
// header renders; an absent one produces an invalid date rendered as text, which
// is a silently wrong page rather than an error, so the guard is a SHAPE check.
test('a malformed user object falls back', async () => {
  withMe()
  server.use(
    http.post('/v1/auth/login', () =>
      HttpResponse.json({
        token: 'tok_3',
        expires_at: '',
        user: { id: 'u-partial', email: 'partial@studio.dev', name: 'P', is_admin: false },
      }),
    ),
  )
  renderProbe()
  await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('anonymous'))
  await userEvent.click(screen.getByText('login'))
  await waitFor(() => expect(screen.getByTestId('email')).toHaveTextContent('endpoint@studio.dev'))
  expect(screen.getByTestId('created')).toHaveTextContent('2026-02-03T04:05:06Z')
  expect(meCalls).toBe(1)
})

// Kills: dropping the is_admin type check from the guard. Seven consumers gate
// privileged UI on user.is_admin truthiness (AdminRoute, HoloShell's nav filter,
// WorkerDetailPage, WorkerLabels, JobDetailPage's canManage, and the two profile
// displays); an absent is_admin key reads as undefined, which is falsy and
// silently demotes an admin for the session rather than erroring.
test('a user missing is_admin falls back to /users/me', async () => {
  withMe()
  server.use(
    http.post('/v1/auth/login', () =>
      HttpResponse.json({
        token: 'tok_5',
        expires_at: '',
        user: {
          id: 'u-partial',
          email: 'partial@studio.dev',
          name: 'P',
          created_at: '2026-01-02T03:04:05Z',
        },
      }),
    ),
  )
  renderProbe()
  await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('anonymous'))
  await userEvent.click(screen.getByText('login'))
  await waitFor(() => expect(screen.getByTestId('email')).toHaveTextContent('endpoint@studio.dev'))
  expect(meCalls).toBe(1)
})

// Kills: dropping the email type check from the guard. Symmetric with the
// is_admin case above - email is a rendered identity field on the same footing.
test('a user missing email falls back to /users/me', async () => {
  withMe()
  server.use(
    http.post('/v1/auth/login', () =>
      HttpResponse.json({
        token: 'tok_6',
        expires_at: '',
        user: {
          id: 'u-partial',
          name: 'P',
          is_admin: false,
          created_at: '2026-01-02T03:04:05Z',
        },
      }),
    ),
  )
  renderProbe()
  await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('anonymous'))
  await userEvent.click(screen.getByText('login'))
  await waitFor(() => expect(screen.getByTestId('email')).toHaveTextContent('endpoint@studio.dev'))
  expect(meCalls).toBe(1)
})

// Mutation-proven gap: dropping the two non-empty-string checks in usableUser
// (id !== '' and created_at !== '') left all 31 auth tests green at review time,
// because no fixture used an empty string - every malformed case used an absent
// key instead. An empty string passes typeof === 'string' but must still fall
// back, since a real row's id and created_at are never empty.
test('a user with an empty id falls back to /users/me', async () => {
  withMe()
  server.use(
    http.post('/v1/auth/login', () =>
      HttpResponse.json({
        token: 'tok_7',
        expires_at: '',
        user: { id: '', email: 'partial@studio.dev', name: 'P', is_admin: false, created_at: '2026-01-02T03:04:05Z' },
      }),
    ),
  )
  renderProbe()
  await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('anonymous'))
  await userEvent.click(screen.getByText('login'))
  await waitFor(() => expect(screen.getByTestId('email')).toHaveTextContent('endpoint@studio.dev'))
  expect(meCalls).toBe(1)
})

test('a user with an empty created_at falls back to /users/me', async () => {
  withMe()
  server.use(
    http.post('/v1/auth/login', () =>
      HttpResponse.json({
        token: 'tok_8',
        expires_at: '',
        user: { id: 'u-partial', email: 'partial@studio.dev', name: 'P', is_admin: false, created_at: '' },
      }),
    ),
  )
  renderProbe()
  await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('anonymous'))
  await userEvent.click(screen.getByText('login'))
  await waitFor(() => expect(screen.getByTestId('email')).toHaveTextContent('endpoint@studio.dev'))
  expect(meCalls).toBe(1)
})

// Symmetric with the empty-id and empty-created_at cases above: an empty string
// passes typeof === 'string' but is not a real email, and email is rendered as
// identity in the same places created_at is.
test('a user with an empty email falls back to /users/me', async () => {
  withMe()
  server.use(
    http.post('/v1/auth/login', () =>
      HttpResponse.json({
        token: 'tok_9',
        expires_at: '',
        user: { id: 'u-partial', email: '', name: 'P', is_admin: false, created_at: '2026-01-02T03:04:05Z' },
      }),
    ),
  )
  renderProbe()
  await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('anonymous'))
  await userEvent.click(screen.getByText('login'))
  await waitFor(() => expect(screen.getByTestId('email')).toHaveTextContent('endpoint@studio.dev'))
  expect(meCalls).toBe(1)
})

// Kills: applying the change to login only. The client cannot distinguish the two
// server register arms - both are one POST /v1/auth/register from this method -
// so this is the whole of what the client can get wrong about register.
test('register uses the user object too', async () => {
  withMe()
  server.use(
    http.post('/v1/auth/register', () =>
      HttpResponse.json({ token: 'tok_4', expires_at: '', user: BODY_USER }, { status: 201 }),
    ),
  )
  renderProbe()
  await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('anonymous'))
  await userEvent.click(screen.getByText('register'))
  await waitFor(() => expect(screen.getByTestId('email')).toHaveTextContent('body@studio.dev'))
  await new Promise((r) => setTimeout(r, 20))
  expect(meCalls).toBe(0)
})
