import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { AuthProvider, useAuth } from '../auth/AuthProvider'
import { apiFetch } from '../lib/api'
import { clearToken, getToken, setToken } from '../lib/token'
import { SessionsTab } from './SessionsTab'

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
  const { status } = useAuth()
  return <span data-testid="status">{status}</span>
}

// A query with an ACTIVE OBSERVER, mounted alongside the tab. Its mount-time
// fetch is the positive control that proves the observer is live; without it the
// "no further calls" assertion in the third test would be vacuous, since a query
// that never had an observer cannot be refetched by invalidateQueries' default
// refetchType:'active' either way.
function StatsProbe() {
  const q = useQuery({
    queryKey: ['jobs', 'stats'],
    queryFn: () => apiFetch<{ running: number }>('/jobs/stats'),
  })
  return <span data-testid="stats">{q.data ? 'loaded' : 'pending'}</span>
}

function renderTab() {
  setToken('tok_live')
  server.use(http.get('/v1/users/me', () => HttpResponse.json(ME)))
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const utils = render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/profile/sessions']}>
        <AuthProvider>
          <SessionProbe />
          <Routes>
            <Route
              path="/profile/sessions"
              element={
                <>
                  <StatsProbe />
                  <SessionsTab />
                </>
              }
            />
            <Route path="/auth" element={<div>SIGN IN SCREEN</div>} />
          </Routes>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return { ...utils, client }
}

async function confirmSignOut() {
  await userEvent.click(screen.getByRole('button', { name: 'Sign out everywhere' }))
  await screen.findByRole('dialog')
  // The dialog's own confirm button, not the page's trigger. ConfirmDialog gives
  // the confirm button the label passed as confirmLabel, so both carry the same
  // accessible name while the dialog is open - take the one inside the dialog.
  const dialog = screen.getByRole('dialog')
  const confirm = within(dialog).getByRole('button', { name: 'Sign out everywhere' })
  await userEvent.click(confirm)
}

test('on 204 the token, the user, the cache AND the route are all torn down', async () => {
  server.use(
    http.delete('/v1/auth/tokens', () => new HttpResponse(null, { status: 204 })),
    http.get('/v1/jobs/stats', () => HttpResponse.json({ running: 1 })),
  )
  const { client } = renderTab()
  await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('authenticated'))
  await waitFor(() => expect(screen.getByTestId('stats')).toHaveTextContent('loaded'))

  // Paired positive BEFORE the action: all four stores are in the pre-state, so
  // every assertion after the action is about the teardown.
  expect(getToken()).toBe('tok_live')
  expect(client.getQueryCache().getAll().length).toBeGreaterThan(0)
  expect(screen.queryByText('SIGN IN SCREEN')).toBeNull()

  await confirmSignOut()

  // FOUR assertions, not one. A test asserting only the navigation passes against
  // an implementation that leaves a live token in localStorage - which is the
  // exact defect this task exists to prevent, and it only surfaces later, at a
  // random request, as a confusing bounce to sign-in.
  await waitFor(() => expect(getToken()).toBeNull())
  expect(await screen.findByText('SIGN IN SCREEN')).toBeInTheDocument()
  expect(screen.getByTestId('status')).toHaveTextContent('anonymous')
  expect(client.getQueryCache().getAll()).toHaveLength(0)
})

test('exactly one DELETE to the PLURAL path, and NEVER the singular one', async () => {
  let plural = 0
  let singular = 0
  server.use(
    http.delete('/v1/auth/tokens', () => {
      plural++
      return new HttpResponse(null, { status: 204 })
    }),
    // Returns 204, not 401, so a stray call is COUNTED rather than cascading into
    // an onUnauthorized teardown that would mask the defect by producing the
    // right-looking end state for the wrong reason.
    http.delete('/v1/auth/token', () => {
      singular++
      return new HttpResponse(null, { status: 204 })
    }),
    http.get('/v1/jobs/stats', () => HttpResponse.json({ running: 1 })),
  )
  renderTab()
  await waitFor(() => expect(screen.getByTestId('status')).toHaveTextContent('authenticated'))

  await confirmSignOut()
  await waitFor(() => expect(getToken()).toBeNull())

  expect(plural).toBe(1)
  // The tell that logout() was reused instead of clearSession(). logout() fires
  // DELETE /v1/auth/token against a token the server destroyed a moment ago - a
  // guaranteed 401 in production, whose onUnauthorized would race the teardown
  // already in flight.
  expect(singular).toBe(0)
})

test('no query refetches against the destroyed credential (Invariant 1)', async () => {
  let statsCalls = 0
  server.use(
    http.delete('/v1/auth/tokens', () => new HttpResponse(null, { status: 204 })),
    http.get('/v1/jobs/stats', () => {
      statsCalls++
      return HttpResponse.json({ running: 1 })
    }),
  )
  renderTab()
  await waitFor(() => expect(screen.getByTestId('stats')).toHaveTextContent('loaded'))
  // Positive control: the observer is live and DID fetch once. Without this the
  // equality below could be about a query that was never mounted.
  expect(statsCalls).toBe(1)

  await confirmSignOut()
  await waitFor(() => expect(getToken()).toBeNull())
  await screen.findByText('SIGN IN SCREEN')

  // The whole invariant in one number. A hook-level onSuccess that invalidated a
  // broad key would refetch this query BEFORE the navigation - the callback runs
  // at query-core mutation.js:123, ahead of the success dispatch and of any
  // unmount - and every one of those requests would carry a credential the server
  // has already destroyed.
  expect(statsCalls).toBe(1)
})
