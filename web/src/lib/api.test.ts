import { http, HttpResponse } from 'msw'
import { afterEach, expect, test, vi } from 'vitest'
import { server } from '../test/setup-helpers'
import { ApiError, apiFetch, onUnauthorized } from './api'
import { clearToken, setToken } from './token'

afterEach(() => clearToken())

test('attaches the bearer token when present', async () => {
  let seen: string | null = null
  server.use(
    http.get('/v1/users/me', ({ request }) => {
      seen = request.headers.get('authorization')
      return HttpResponse.json({ id: '1', email: 'a@b.co', name: 'A', role: 'user' })
    }),
  )
  setToken('tok_123')
  await apiFetch('/users/me')
  expect(seen).toBe('Bearer tok_123')
})

test('parses the error envelope into ApiError', async () => {
  server.use(
    http.post('/v1/auth/login', () =>
      HttpResponse.json({ error: 'invalid_credentials' }, { status: 401 }),
    ),
  )
  const err = await apiFetch('/auth/login', { method: 'POST', json: {} }).catch((e) => e)
  expect(err).toBeInstanceOf(ApiError)
  expect((err as ApiError).status).toBe(401)
  expect((err as ApiError).code).toBe('invalid_credentials')
})

test('invokes the unauthorized handler on 401', async () => {
  server.use(
    http.get('/v1/users/me', () =>
      HttpResponse.json({ error: 'invalid_credentials' }, { status: 401 }),
    ),
  )
  const spy = vi.fn()
  const off = onUnauthorized(spy)
  await apiFetch('/users/me').catch(() => {})
  expect(spy).toHaveBeenCalledOnce()
  off()
})

test('surfaces 429 as ApiError with status 429', async () => {
  server.use(
    http.post('/v1/auth/login', () =>
      HttpResponse.json({ error: 'rate_limited' }, { status: 429 }),
    ),
  )
  const err = await apiFetch('/auth/login', { method: 'POST', json: {} }).catch((e) => e)
  expect((err as ApiError).status).toBe(429)
})

test('returns undefined for a 204 No Content response', async () => {
  server.use(http.delete('/v1/workers/w1/token', () => new HttpResponse(null, { status: 204 })))
  const out = await apiFetch<void>('/workers/w1/token', { method: 'DELETE' })
  expect(out).toBeUndefined()
})

test('returns undefined for a 202 Accepted empty-body response', async () => {
  server.use(http.post('/v1/workers/w1/workspaces/ws-a/evict', () => new HttpResponse(null, { status: 202 })))
  const out = await apiFetch<void>('/workers/w1/workspaces/ws-a/evict', { method: 'POST' })
  expect(out).toBeUndefined()
})

// The 401 notification must say WHICH credential produced it, or the subscriber
// cannot tell a 401 belonging to the session it holds from one belonging to a
// session that died two logins ago. See
// docs/superpowers/plans/2026-08-13-cross-generation-401.md.
test('the 401 listener receives the exact token the request carried', async () => {
  server.use(
    http.get('/v1/users/me', () =>
      HttpResponse.json({ error: 'invalid_credentials' }, { status: 401 }),
    ),
  )
  setToken('tok_stamped')
  const spy = vi.fn()
  const off = onUnauthorized(spy)
  await apiFetch('/users/me').catch(() => {})
  // toHaveBeenCalledWith, not just toHaveBeenCalled: fn() with no argument is the
  // pre-fix behaviour and passes any arity-blind assertion.
  expect(spy).toHaveBeenCalledWith('tok_stamped')
  off()
})

test('the 401 listener receives null when the request carried no token', async () => {
  server.use(
    http.post('/v1/auth/login', () =>
      HttpResponse.json({ error: 'invalid_credentials' }, { status: 401 }),
    ),
  )
  // No setToken: this is the failed-login-on-the-sign-in-screen shape, and the
  // subscriber must be able to see that the request was anonymous rather than
  // guess. null, explicitly - NOT undefined, which is what an unstamped fn()
  // would deliver and which would compare unequal to getToken()'s null.
  const spy = vi.fn()
  const off = onUnauthorized(spy)
  await apiFetch('/auth/login', { method: 'POST', json: {} }).catch(() => {})
  expect(spy).toHaveBeenCalledWith(null)
  off()
})

// unauthorizedListeners is a Set iterated with forEach: an uncaught throw from one
// subscriber stops iteration (later subscribers never run) AND replaces the
// caller's real result - the ApiError apiFetch is about to throw for this 401 -
// with the subscriber's unrelated error. Neither is acceptable: one buggy
// subscriber (of which AuthProvider is only one; more can register) must not be
// able to silently stop every other subscriber, or turn every 401 anywhere in the
// app into a thrown Error the calling code was never written to expect.
test('a throwing onUnauthorized listener does not stop delivery to the others or replace the ApiError', async () => {
  server.use(
    http.get('/v1/users/me', () =>
      HttpResponse.json({ error: 'invalid_credentials' }, { status: 401 }),
    ),
  )
  const throwing = vi.fn(() => {
    throw new Error('boom')
  })
  const after = vi.fn()
  const offThrowing = onUnauthorized(throwing)
  const offAfter = onUnauthorized(after)
  // The thrown error is expected to be logged (that is the fix); spy on
  // console.error so the expected log line does not read as test-output noise.
  const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
  const err = await apiFetch('/users/me').catch((e) => e)
  expect(throwing).toHaveBeenCalledOnce()
  expect(after).toHaveBeenCalledOnce()
  expect(err).toBeInstanceOf(ApiError)
  expect((err as ApiError).status).toBe(401)
  errorSpy.mockRestore()
  offThrowing()
  offAfter()
})
