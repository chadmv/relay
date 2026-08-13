import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { ApiError } from '../lib/api'
import { changePassword, signOutEverywhere, updateMe } from './api'

const ME = {
  id: 'u1',
  email: 'mira@studio.dev',
  name: 'Mira Sato',
  is_admin: true,
  created_at: '2025-04-02T09:15:00Z',
  archived_at: null,
}

test('updateMe PATCHes /v1/users/me with exactly { name } and parses the row', async () => {
  let method: string | undefined
  let path: string | undefined
  let body: Record<string, unknown> | undefined
  server.use(
    http.patch('/v1/users/me', async ({ request }) => {
      method = request.method
      path = new URL(request.url).pathname
      body = (await request.json()) as Record<string, unknown>
      return HttpResponse.json({ ...ME, name: 'Mira Renamed' })
    }),
  )
  const u = await updateMe('Mira Renamed')
  expect(method).toBe('PATCH')
  expect(path).toBe('/v1/users/me')
  // toEqual on the WHOLE body, not a property check: the failure mode is an EXTRA
  // key. updateUserRequest has exactly one field (internal/api/users.go:49-51)
  // and email is immutable store-wide, so an `email` key here would be a control
  // that silently does nothing.
  expect(body).toEqual({ name: 'Mira Renamed' })
  expect(u.name).toBe('Mira Renamed')
  // created_at is on the type and on the wire; the header renders it.
  expect(u.created_at).toBe('2025-04-02T09:15:00Z')
})

test('updateMe surfaces the empty-name 400 as ApiError with the server sentence', async () => {
  server.use(
    http.patch('/v1/users/me', () =>
      HttpResponse.json({ error: 'name is required' }, { status: 400 }),
    ),
  )
  const err = await updateMe('   ').catch((e) => e)
  expect(err).toBeInstanceOf(ApiError)
  expect(err.status).toBe(400)
  // ApiError.message is "<status> <server sentence>" (lib/api.ts:53), which is
  // what the form renders verbatim (ResetPasswordDialog.tsx:84).
  expect(err.message).toBe('400 name is required')
})

test('changePassword PUTs exactly current_password and new_password, and NOTHING else', async () => {
  let method: string | undefined
  let path: string | undefined
  let body: Record<string, unknown> | undefined
  server.use(
    http.put('/v1/users/me/password', async ({ request }) => {
      method = request.method
      path = new URL(request.url).pathname
      body = (await request.json()) as Record<string, unknown>
      return new HttpResponse(null, { status: 204 })
    }),
  )
  await expect(changePassword('old-secret', 'new-secret-123')).resolves.toBeUndefined()
  expect(method).toBe('PUT')
  expect(path).toBe('/v1/users/me/password')
  expect(body).toEqual({ current_password: 'old-secret', new_password: 'new-secret-123' })
  // The form has THREE fields and the API takes TWO. A confirm_password key is
  // the natural mistake; assert the key set so it cannot pass.
  expect(Object.keys(body!).sort()).toEqual(['current_password', 'new_password'])
})

test('changePassword surfaces a wrong current password as ApiError(403), not 401', async () => {
  // 403 is deliberate on the server (internal/api/auth.go:298-301). A 401 would
  // fire onUnauthorized (lib/api.ts:44-46) and sign the user out on a typo.
  server.use(
    http.put('/v1/users/me/password', () =>
      HttpResponse.json({ error: 'current password is incorrect' }, { status: 403 }),
    ),
  )
  const err = await changePassword('wrong', 'new-secret-123').catch((e) => e)
  expect(err).toBeInstanceOf(ApiError)
  expect(err.status).toBe(403)
  expect(err.message).toBe('403 current password is incorrect')
})

test('signOutEverywhere DELETEs the PLURAL path and tolerates the empty 204', async () => {
  let method: string | undefined
  let path: string | undefined
  let singularCalls = 0
  server.use(
    http.delete('/v1/auth/tokens', ({ request }) => {
      method = request.method
      path = new URL(request.url).pathname
      // A real 204 has NO body (internal/api/auth.go:356). A client that
      // unconditionally calls res.json() throws 'Unexpected end of JSON input'.
      return new HttpResponse(null, { status: 204 })
    }),
    http.delete('/v1/auth/token', () => {
      singularCalls++
      return new HttpResponse(null, { status: 204 })
    }),
  )
  await expect(signOutEverywhere()).resolves.toBeUndefined()
  expect(method).toBe('DELETE')
  // Singular vs plural is a one-character difference between "revoke my current
  // token" (auth.go:341-348) and "revoke every token this user has"
  // (auth.go:350-357). Assert the exact path AND that the sibling never fired.
  expect(path).toBe('/v1/auth/tokens')
  expect(singularCalls).toBe(0)
})
