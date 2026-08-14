import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { assertNoConsoleLeak, spyOnConsole } from '../test/secretLeaks'
import { apiFetch } from '../lib/api'
import { clearToken, setToken } from '../lib/token'
import { AuthProvider, useAuth } from './AuthProvider'

afterEach(() => clearToken())

// A distinctive stand-in for the real bearer credential, same shape as the
// project's other secrecy tests (enrollmentTokenSecrecy.test.tsx,
// inviteTokenSecrecy.test.tsx).
const TOKEN = 'f00dcafe'.repeat(8)

const ME = {
  id: 'u1',
  email: 'mira@studio.dev',
  name: 'Mira Sato',
  is_admin: true,
  created_at: '2025-04-02T09:15:00Z',
  archived_at: null,
}

function Probe() {
  const { user } = useAuth()
  return <span data-testid="who">{user?.email ?? 'none'}</span>
}

// apiFetch and apiStream stamp every onUnauthorized notification with the raw
// bearer token (web/src/lib/api.ts) so subscribers can fence on session identity
// - a channel this project did not have before the cross-generation-401 fix.
// Every registered subscriber, including AuthProvider's and any future one,
// receives that raw string. Nothing in the mechanism forces a subscriber to log
// it, but nothing prevents it either. This pins the REAL flow - a live
// AuthProvider mounted, a real 401 dispatched - never lets it reach console in
// any representation, using the same instrument (secretLeaks.ts) the enrollment
// and invite token flows are already pinned with, rather than a new pattern.
test('the session token never reaches console through a 401', async () => {
  const spies = spyOnConsole()
  server.use(http.get('/v1/users/me', () => HttpResponse.json(ME)))
  setToken(TOKEN)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <AuthProvider>
        <Probe />
      </AuthProvider>
    </QueryClientProvider>,
  )
  await waitFor(() => expect(screen.getByTestId('who')).toHaveTextContent('mira@studio.dev'))

  server.use(
    http.get('/v1/jobs/stats', () =>
      HttpResponse.json({ error: 'invalid token' }, { status: 401 }),
    ),
  )
  await apiFetch('/jobs/stats').catch(() => {})
  // Positive control on the setup: the 401 really reached AuthProvider's real
  // listener and really tore the session down - otherwise this test would pass
  // vacuously because the flow under test never ran.
  await waitFor(() => expect(screen.getByTestId('who')).toHaveTextContent('none'))

  assertNoConsoleLeak(spies, TOKEN)
  spies.forEach((s) => s.mockRestore())
})
