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
