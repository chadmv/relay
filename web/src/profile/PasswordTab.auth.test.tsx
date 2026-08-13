import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { AuthProvider, useAuth } from '../auth/AuthProvider'
import { clearToken, getToken, setToken } from '../lib/token'
import { PasswordTab } from './PasswordTab'

afterEach(() => clearToken())

const ME = {
  id: 'u1',
  email: 'mira@studio.dev',
  name: 'Mira Sato',
  is_admin: true,
  created_at: '2025-04-02T09:15:00Z',
  archived_at: null,
}

function SessionProbe() {
  const { status, user } = useAuth()
  return (
    <div>
      <span data-testid="status">{status}</span>
      <span data-testid="who">{user?.email ?? 'none'}</span>
    </div>
  )
}

function renderTab() {
  setToken('tok_live')
  server.use(http.get('/v1/users/me', () => HttpResponse.json(ME)))
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const utils = render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/profile/password']}>
        <AuthProvider>
          <SessionProbe />
          <PasswordTab />
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return { ...utils, client }
}

async function submit(current: string, next: string) {
  await userEvent.type(screen.getByLabelText('Current password'), current)
  await userEvent.type(screen.getByLabelText('New password'), next)
  await userEvent.type(screen.getByLabelText('Confirm new password'), next)
  await userEvent.click(screen.getByRole('button', { name: 'Update password' }))
}

test('a 403 renders inline and leaves the user SIGNED IN', async () => {
  // 403, not 401, is the server's own choice (internal/api/auth.go:298-301), and
  // onUnauthorized is 401-only (web/src/lib/api.ts:44-46). Asserting a specific
  // 403 is the discriminating input: a test using a generic error mock would pass
  // against a component that signs the user out on ANY failure.
  server.use(
    http.put('/v1/users/me/password', () =>
      HttpResponse.json({ error: 'current password is incorrect' }, { status: 403 }),
    ),
  )
  renderTab()
  await waitFor(() => expect(screen.getByTestId('who')).toHaveTextContent('mira@studio.dev'))

  await submit('wrong-password', 'new-secret-123')

  expect(await screen.findByRole('alert')).toHaveTextContent('403 current password is incorrect')
  // All three stores, not just one: an implementation that navigated away without
  // clearing the token, or cleared the token without navigating, would pass a
  // single-store assertion.
  expect(getToken()).toBe('tok_live')
  expect(screen.getByTestId('status')).toHaveTextContent('authenticated')
  expect(screen.getByTestId('who')).toHaveTextContent('mira@studio.dev')
})

test('a 401 from the same endpoint DOES tear the session down (control: the probe is live)', async () => {
  // Without this, "still signed in after a 403" passes against a harness in which
  // nothing could ever sign anybody out. This proves the instrument works, so the
  // assertion above is about the 403 and not about a dead probe.
  server.use(
    http.put('/v1/users/me/password', () =>
      HttpResponse.json({ error: 'invalid token' }, { status: 401 }),
    ),
  )
  renderTab()
  await waitFor(() => expect(screen.getByTestId('who')).toHaveTextContent('mira@studio.dev'))

  await submit('old-secret', 'new-secret-123')

  await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('anonymous'))
  expect(getToken()).toBeNull()
})

test('a 204 leaves the user SIGNED IN - this endpoint spares the caller token', async () => {
  // The direct counterpart of SessionsTab.teardown.test.tsx, which asserts the
  // opposite for DELETE /v1/auth/tokens. These two tests are each other's control
  // and the difference between them is the whole session story of this slice:
  // DeleteOtherTokensForUser has `AND id <> $2`
  // (internal/store/query/tokens.sql:28-29); DeleteTokensForUser does not (:25-26).
  server.use(http.put('/v1/users/me/password', () => new HttpResponse(null, { status: 204 })))
  renderTab()
  await waitFor(() => expect(screen.getByTestId('who')).toHaveTextContent('mira@studio.dev'))

  await submit('old-secret', 'new-secret-123')

  await waitFor(() => expect(screen.getByRole('status')).toBeInTheDocument())
  expect(getToken()).toBe('tok_live')
  expect(screen.getByTestId('status')).toHaveTextContent('authenticated')
})

test('the settled mutation does not retain the plaintext password anywhere', async () => {
  server.use(http.put('/v1/users/me/password', () => new HttpResponse(null, { status: 204 })))
  const { client } = renderTab()
  await waitFor(() => expect(screen.getByTestId('who')).toHaveTextContent('mira@studio.dev'))

  const secret = 'correct-horse-battery-staple'
  await submit('old-secret', secret)
  await waitFor(() => expect(screen.getByRole('status')).toBeInTheDocument())

  const mutations = client.getMutationCache().getAll()
  // POSITIVE CONTROL FIRST. TanStack keeps a settled mutation in the cache for
  // the 5-minute default gcTime, so this list must be non-empty - otherwise the
  // absence assertion below is about an empty array and proves nothing.
  expect(mutations.length).toBeGreaterThan(0)
  for (const m of mutations) {
    // state.variables is where mutate(x) puts x (query-core mutation.js:94).
    // Passing the password as a variable is the plausible implementation and is
    // exactly what this forbids. Stringify the whole state so `data`, `context`
    // and any nested carrier are covered too.
    expect(JSON.stringify(m.state)).not.toContain(secret)
    expect(m.state.variables).toBeUndefined()
  }
  // Second store: the cleared inputs. Necessary but NOT sufficient - calling a
  // clear function is not evidence, which is why the cache assertion comes first.
  expect(screen.getByLabelText('New password')).toHaveValue('')
})
