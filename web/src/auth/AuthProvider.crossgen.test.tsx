import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { useState } from 'react'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { apiFetch } from '../lib/api'
import { clearToken, getToken, setToken } from '../lib/token'
import { AuthProvider, useAuth } from './AuthProvider'

afterEach(() => clearToken())

const ME = {
  id: 'u1',
  email: 'mira@studio.dev',
  name: 'Mira Sato',
  is_admin: true,
  created_at: '2025-04-02T09:15:00Z',
  archived_at: null,
}

// The in-flight request under test. It is held in a module-level binding rather
// than in component state ON PURPOSE: the test awaits this exact promise, so the
// interleaving is controlled by an explicit gate and a real await, never by a
// timer or a sleep. A timing-based version of this test would be flaky and this
// project has rejected those before.
let inflight: Promise<unknown> | null = null

function Probe() {
  const { status, user, login, clearSession } = useAuth()
  const [fired, setFired] = useState(0)
  return (
    <div>
      <span data-testid="status">{status}</span>
      <span data-testid="who">{user?.email ?? 'none'}</span>
      <span data-testid="fired">{fired}</span>
      <button
        onClick={() => {
          // apiFetch reads the token at call time, so this request is stamped with
          // whatever is stored RIGHT NOW - which is the whole point.
          inflight = apiFetch('/jobs/stats').catch(() => {})
          setFired((n) => n + 1)
        }}
      >
        fire
      </button>
      <button onClick={() => clearSession()}>clear</button>
      <button onClick={() => void login('mira@studio.dev', 'pw').catch(() => {})}>login</button>
    </div>
  )
}

function renderProbe() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const utils = render(
    <QueryClientProvider client={client}>
      <AuthProvider>
        <Probe />
      </AuthProvider>
    </QueryClientProvider>,
  )
  return { ...utils, client }
}

/** A 401 for /jobs/stats that does not resolve until the returned release() is called. */
function gated401() {
  let release!: () => void
  const gate = new Promise<void>((resolve) => {
    release = resolve
  })
  const tokensSeen: (string | null)[] = []
  server.use(
    http.get('/v1/jobs/stats', async ({ request }) => {
      tokensSeen.push(request.headers.get('Authorization'))
      await gate
      return HttpResponse.json({ error: 'invalid token' }, { status: 401 })
    }),
  )
  return { release: () => release(), tokensSeen }
}

test('a 401 from a DEAD session does not clear the session that replaced it', async () => {
  // THE discriminating case, and the reason this file exists. Proven RED against
  // a listener whose only guard is the anonymous check: there, the late 401 calls
  // clearToken() on token B and the user watches a successful login undo itself
  // with no error message.
  server.use(
    http.get('/v1/users/me', () => HttpResponse.json(ME)),
    http.post('/v1/auth/login', () => HttpResponse.json({ token: 'tok_B', expires_at: '' })),
  )
  setToken('tok_A')
  renderProbe()
  await waitFor(() => expect(screen.getByTestId('who')).toHaveTextContent('mira@studio.dev'))

  const { release, tokensSeen } = gated401()
  await userEvent.click(screen.getByText('fire'))
  // Positive control on the SETUP: the held request really went out, and it really
  // carried token A. Without this the test could pass because no request was ever
  // made, which is the vacuous version of it.
  await waitFor(() => expect(tokensSeen).toEqual(['Bearer tok_A']))

  // Generation A ends, generation B begins - the sign-out-everywhere shape.
  await userEvent.click(screen.getByText('clear'))
  await waitFor(() => expect(getToken()).toBeNull())
  await userEvent.click(screen.getByText('login'))
  await waitFor(() => expect(getToken()).toBe('tok_B'))
  await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('authenticated'))

  // Only NOW does the dead session's 401 land.
  release()
  await act(async () => {
    await inflight
  })

  // Session B survives, in both stores. getToken() is the load-bearing one: the
  // listener's clearToken() is synchronous, so this assertion needs no React
  // commit and cannot pass by racing one.
  expect(getToken()).toBe('tok_B')
  expect(screen.getByTestId('status')).toHaveTextContent('authenticated')
  expect(screen.getByTestId('who')).toHaveTextContent('mira@studio.dev')
})

test('POSITIVE CONTROL: a 401 for the CURRENT token still tears the session down', async () => {
  // The other half of the pair, in the same file and the same harness as the test
  // above, so a reader sees both at once. Without it, "the 401 was ignored" would
  // also be satisfied by a listener that ignores every 401 - which is the single
  // most likely way to get this fix wrong.
  server.use(http.get('/v1/users/me', () => HttpResponse.json(ME)))
  setToken('tok_A')
  renderProbe()
  await waitFor(() => expect(screen.getByTestId('who')).toHaveTextContent('mira@studio.dev'))

  const { release, tokensSeen } = gated401()
  await userEvent.click(screen.getByText('fire'))
  await waitFor(() => expect(tokensSeen).toEqual(['Bearer tok_A']))

  // No teardown, no re-login: token A is still the session when the 401 lands.
  release()
  await act(async () => {
    await inflight
  })

  expect(getToken()).toBeNull()
  await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('anonymous'))
  expect(screen.getByTestId('who')).toHaveTextContent('none')
})

test('a 401 landing DURING teardown still leaves everything torn down', async () => {
  // The convergence property the backlog item said three Phase 4 lanes confirmed.
  // It still holds, but for a DIFFERENT reason after this change: the listener no
  // longer runs at all here (the token is already gone, so the identity fence
  // rejects), and convergence now rests entirely on clearSession having done all
  // four things itself, synchronously, with clearToken() first
  // (AuthProvider.tsx:127-132). Pin it, because that is a load-bearing dependency
  // the fix silently acquired.
  server.use(http.get('/v1/users/me', () => HttpResponse.json(ME)))
  setToken('tok_A')
  const { client } = renderProbe()
  await waitFor(() => expect(screen.getByTestId('who')).toHaveTextContent('mira@studio.dev'))
  client.setQueryData(['workers'], [{ id: 'w1' }])

  const { release, tokensSeen } = gated401()
  await userEvent.click(screen.getByText('fire'))
  await waitFor(() => expect(tokensSeen).toEqual(['Bearer tok_A']))

  await userEvent.click(screen.getByText('clear'))
  release()
  await act(async () => {
    await inflight
  })

  expect(getToken()).toBeNull()
  expect(screen.getByTestId('status')).toHaveTextContent('anonymous')
  expect(screen.getByTestId('who')).toHaveTextContent('none')
  expect(client.getQueryCache().getAll()).toHaveLength(0)
})
