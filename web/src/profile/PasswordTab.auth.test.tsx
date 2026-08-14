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
  // onUnauthorized is 401-only (the onUnauthorized notifier in lib/api.ts).
  // Asserting a specific 403 is the discriminating input: a test using a generic
  // error mock would pass against a component that signs the user out on ANY failure.
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
  // (internal/store/query/tokens.sql:43-44); DeleteTokensForUser does not (:40-41).
  server.use(http.put('/v1/users/me/password', () => new HttpResponse(null, { status: 204 })))
  renderTab()
  await waitFor(() => expect(screen.getByTestId('who')).toHaveTextContent('mira@studio.dev'))

  await submit('old-secret', 'new-secret-123')

  await waitFor(() => expect(screen.getByRole('status')).toBeInTheDocument())
  expect(getToken()).toBe('tok_live')
  expect(screen.getByTestId('status')).toHaveTextContent('authenticated')
})

test('the settled mutation does not retain the plaintext password anywhere, and is evicted promptly', async () => {
  // The PUT is held open so there is a stable window to inspect the mutation
  // cache WHILE PENDING - this is the positive control, moved earlier because
  // the eviction fix below (gcTime: 0 + reset() in onSuccess) means the old
  // after-settle window can no longer be relied on to still hold an entry.
  let release: (() => void) | undefined
  const gate = new Promise<void>((resolve) => {
    release = resolve
  })
  server.use(
    http.put('/v1/users/me/password', async () => {
      await gate
      return new HttpResponse(null, { status: 204 })
    }),
  )
  const { client } = renderTab()
  await waitFor(() => expect(screen.getByTestId('who')).toHaveTextContent('mira@studio.dev'))

  const secret = 'correct-horse-battery-staple'
  await userEvent.type(screen.getByLabelText('Current password'), 'old-secret')
  await userEvent.type(screen.getByLabelText('New password'), secret)
  await userEvent.type(screen.getByLabelText('Confirm new password'), secret)
  await userEvent.click(screen.getByRole('button', { name: 'Update password' }))

  // POSITIVE CONTROL FIRST. While the PUT is still in flight the mutation IS
  // in the cache, so this list must be non-empty here - otherwise the absence
  // assertions below (and the eventual-eviction assertion further down) would
  // be about an empty array and prove nothing.
  await waitFor(() => expect(client.getMutationCache().getAll().length).toBeGreaterThan(0))
  const pending = client.getMutationCache().getAll()
  for (const m of pending) {
    // state.variables is where mutate(x) puts x (query-core mutation.js:94).
    // Passing the password as a variable is the plausible implementation and is
    // exactly what this forbids. Stringify the whole state so `data`, `context`
    // and any nested carrier are covered too.
    expect(JSON.stringify(m.state)).not.toContain(secret)
    expect(m.state.variables).toBeUndefined()
  }

  release!()
  await waitFor(() => expect(screen.getByRole('status')).toBeInTheDocument())

  // gcTime: 0 on this mutation plus change.reset() in onSuccess: the settled,
  // now-observer-less mutation becomes eligible for cache removal on the very
  // next tick (same precedent as useAgentEnrollmentActions.ts's create
  // mutation). The plaintext must not merely be absent from what remains - the
  // cache itself must go empty, or it would still be reachable from a devtools
  // heap snapshot for the default 5-minute gcTime.
  await waitFor(() => expect(client.getMutationCache().getAll()).toHaveLength(0))

  // Second store: the cleared inputs. Necessary but NOT sufficient - calling a
  // clear function is not evidence, which is why the cache assertions come first.
  expect(screen.getByLabelText('New password')).toHaveValue('')
})
