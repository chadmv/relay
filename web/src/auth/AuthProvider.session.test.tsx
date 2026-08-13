import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { clearToken, getToken, setToken } from '../lib/token'
import { AuthProvider, useAuth } from './AuthProvider'

afterEach(() => clearToken())

const ME = {
  id: 'u1',
  email: 'mira@studio.dev',
  name: 'Mira Sato',
  is_admin: true,
  created_at: '2025-04-02T09:15:00Z',
}

function Probe() {
  const { status, user, clearSession, applyUser } = useAuth()
  return (
    <div>
      <span data-testid="status">{status}</span>
      <span data-testid="name">{user?.name ?? 'none'}</span>
      <button onClick={() => clearSession()}>clear</button>
      <button onClick={() => applyUser({ ...ME, name: 'Mira Renamed' })}>apply</button>
    </div>
  )
}

function renderProbe(client = new QueryClient()) {
  return render(
    <QueryClientProvider client={client}>
      <AuthProvider>
        <Probe />
      </AuthProvider>
    </QueryClientProvider>,
  )
}

test('clearSession clears token, user, status and the query cache', async () => {
  server.use(http.get('/v1/users/me', () => HttpResponse.json(ME)))
  setToken('tok_existing')
  const qc = new QueryClient()
  qc.setQueryData(['workers'], [{ id: 'w1' }])
  renderProbe(qc)
  await waitFor(() => expect(screen.getByTestId('name')).toHaveTextContent('Mira Sato'))
  // Paired positive: the three stores are non-empty BEFORE the action, so each
  // assertion below is about the teardown and not about a store that was never
  // populated.
  expect(getToken()).toBe('tok_existing')
  expect(qc.getQueryCache().getAll().length).toBeGreaterThan(0)

  await userEvent.click(screen.getByText('clear'))

  await waitFor(() => expect(getToken()).toBeNull())
  expect(screen.getByTestId('name')).toHaveTextContent('none')
  expect(screen.getByTestId('status')).toHaveTextContent('anonymous')
  expect(qc.getQueryCache().getAll()).toHaveLength(0)
})

test('clearSession issues NO network request', async () => {
  let meCalls = 0
  let revokeCalls = 0
  server.use(
    http.get('/v1/users/me', () => {
      meCalls++
      return HttpResponse.json(ME)
    }),
    // Registered so a stray call is COUNTED rather than blowing up as an
    // unhandled request. onUnhandledRequest:'error' would also catch it, but a
    // count says which route was hit.
    http.delete('/v1/auth/token', () => {
      revokeCalls++
      return new HttpResponse(null, { status: 204 })
    }),
    http.delete('/v1/auth/tokens', () => {
      revokeCalls++
      return new HttpResponse(null, { status: 204 })
    }),
  )
  setToken('tok_existing')
  renderProbe()
  await waitFor(() => expect(screen.getByTestId('name')).toHaveTextContent('Mira Sato'))
  expect(meCalls).toBe(1)

  await userEvent.click(screen.getByText('clear'))
  await waitFor(() => expect(getToken()).toBeNull())

  // The whole point of clearSession existing separately from logout(): its one
  // caller has already destroyed every token server-side, so any request made
  // after that point is a guaranteed 401 racing this teardown.
  expect(revokeCalls).toBe(0)
  expect(meCalls).toBe(1)
})

test('applyUser replaces the user row in place with no refetch', async () => {
  let meCalls = 0
  server.use(
    http.get('/v1/users/me', () => {
      meCalls++
      return HttpResponse.json(ME)
    }),
  )
  setToken('tok_existing')
  renderProbe()
  await waitFor(() => expect(screen.getByTestId('name')).toHaveTextContent('Mira Sato'))

  await userEvent.click(screen.getByText('apply'))

  await waitFor(() => expect(screen.getByTestId('name')).toHaveTextContent('Mira Renamed'))
  // The PATCH response IS the authoritative row (internal/api/users.go:429 and
  // :410 both call toUserResponse), so there is nothing to confirm with a second
  // round trip. A confirming refetch would be a second source of truth.
  expect(meCalls).toBe(1)
  expect(screen.getByTestId('status')).toHaveTextContent('authenticated')
})

test('logout STILL issues DELETE /v1/auth/token exactly once after the extraction', async () => {
  // Positive control on the extraction: re-expressing logout() through
  // clearSession must not gut its network half. AuthProvider.test.tsx already
  // covers logout's local effects and must stay byte-identical; this asserts the
  // request itself, which that file does not count.
  let revokeCalls = 0
  server.use(
    http.get('/v1/users/me', () => HttpResponse.json(ME)),
    http.delete('/v1/auth/token', () => {
      revokeCalls++
      return new HttpResponse(null, { status: 204 })
    }),
  )
  setToken('tok_existing')
  function LogoutProbe() {
    const { logout, user } = useAuth()
    return (
      <div>
        <span data-testid="lname">{user?.name ?? 'none'}</span>
        <button onClick={() => logout()}>logout</button>
      </div>
    )
  }
  render(
    <QueryClientProvider client={new QueryClient()}>
      <AuthProvider>
        <LogoutProbe />
      </AuthProvider>
    </QueryClientProvider>,
  )
  await waitFor(() => expect(screen.getByTestId('lname')).toHaveTextContent('Mira Sato'))
  await userEvent.click(screen.getByText('logout'))
  await waitFor(() => expect(getToken()).toBeNull())
  expect(revokeCalls).toBe(1)
})
