import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, test, vi } from 'vitest'
import { server } from './test/setup-helpers'
import { clearToken, getToken, setToken } from './lib/token'
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
    http.get('/v1/jobs', () => HttpResponse.json({ items: [], next_cursor: '', total: 0 })),
    http.get('/v1/jobs/stats', () => HttpResponse.json({ running: 0, queued: 0, done_24h: 0, failed_24h: 0 })),
  )
  render(<App />)
  await waitFor(() =>
    expect(screen.getByText(/sign in to the coordinator/i)).toBeInTheDocument(),
  )
  await userEvent.type(screen.getByLabelText('Email'), 'admin@example.com')
  await userEvent.type(screen.getByLabelText('Password'), 'changeme123')
  await userEvent.click(screen.getByRole('button', { name: /sign in/i }))
  expect(await screen.findByText('OVERVIEW')).toBeInTheDocument()
})

test('a 401 on the next poll lands an authenticated session on sign-in with no loop', async () => {
  const ME = { id: '1', email: 'admin@example.com', name: 'Admin', is_admin: true }
  // Phase 1: authenticated. Hydrate the user and serve empty jobs so we reach OVERVIEW.
  server.use(
    http.get('/v1/users/me', () => HttpResponse.json(ME)),
    http.get('/v1/jobs', () => HttpResponse.json({ items: [], next_cursor: '', total: 0 })),
    http.get('/v1/jobs/stats', () =>
      HttpResponse.json({ running: 0, queued: 0, done_24h: 0, failed_24h: 0 }),
    ),
  )
  setToken('tok')
  render(<App />)
  expect(await screen.findByText('OVERVIEW')).toBeInTheDocument()

  // Phase 2: the session expires. Every endpoint now 401s.
  const unauthorized = () =>
    HttpResponse.json({ error: 'unauthorized' }, { status: 401 })
  server.use(
    http.get('/v1/users/me', unauthorized),
    http.get('/v1/jobs', unauthorized),
    http.get('/v1/jobs/stats', unauthorized),
  )

  // Fire the next jobs poll (useJobs default refetchInterval is 3000ms).
  vi.useFakeTimers()
  await vi.advanceTimersByTimeAsync(3100)
  vi.useRealTimers()

  // The 401 handler must reset auth state: sign-in renders, token gone.
  await waitFor(() =>
    expect(screen.getByText(/sign in to the coordinator/i)).toBeInTheDocument(),
  )
  expect(getToken()).toBeNull()

  // No loop: the authenticated marker must not bounce back.
  expect(screen.queryByText('OVERVIEW')).not.toBeInTheDocument()
  await new Promise((r) => setTimeout(r, 50))
  expect(screen.queryByText('OVERVIEW')).not.toBeInTheDocument()
})
