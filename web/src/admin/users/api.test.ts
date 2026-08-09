import { expect, test } from 'vitest'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/setup-helpers'
import {
  archiveUser,
  createUser,
  listUsers,
  renameUser,
  resetUserPassword,
  unarchiveUser,
} from './api'

const USER = {
  id: 'u1',
  email: 'a@b.co',
  name: 'A',
  is_admin: false,
  created_at: '2026-08-01T00:00:00Z',
  archived_at: null,
}

test('listUsers sends sort and limit=50 and returns the envelope', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/users', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [USER], next_cursor: 'c2', total: 7 })
    }),
  )
  const page = await listUsers({ sort: '-created_at', includeArchived: false, cursor: '', email: '' })
  expect(params?.get('sort')).toBe('-created_at')
  expect(params?.get('limit')).toBe('50')
  expect(params?.has('include_archived')).toBe(false)
  expect(params?.has('cursor')).toBe(false)
  expect(params?.has('email')).toBe(false)
  expect(page.items[0].email).toBe('a@b.co')
  expect(page.next_cursor).toBe('c2')
  expect(page.total).toBe(7)
})

test('listUsers sends include_archived, cursor, and email when provided', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/users', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  await listUsers({ sort: 'email', includeArchived: true, cursor: 'cur1', email: 'a@b.co' })
  expect(params?.get('include_archived')).toBe('true')
  expect(params?.get('cursor')).toBe('cur1')
  expect(params?.get('email')).toBe('a@b.co')
  expect(params?.get('sort')).toBe('email')
})

test('createUser POSTs /users with the full body and reads the 201', async () => {
  let body: unknown
  server.use(
    http.post('/v1/users', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ ...USER, is_admin: true }, { status: 201 })
    }),
  )
  const created = await createUser({ email: 'new@b.co', name: 'New', password: 'password1', is_admin: true })
  expect(body).toEqual({ email: 'new@b.co', name: 'New', password: 'password1', is_admin: true })
  expect(created.is_admin).toBe(true)
})

test('renameUser PATCHes /users/{id} with only a name', async () => {
  let body: unknown
  server.use(
    http.patch('/v1/users/u1', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ ...USER, name: 'Renamed' })
    }),
  )
  const updated = await renameUser('u1', 'Renamed')
  expect(body).toEqual({ name: 'Renamed' })
  expect(updated.name).toBe('Renamed')
})

test('archiveUser POSTs /users/{id}/archive and returns the archived row', async () => {
  server.use(
    http.post('/v1/users/u1/archive', () =>
      HttpResponse.json({ ...USER, archived_at: '2026-08-02T00:00:00Z' }),
    ),
  )
  const archived = await archiveUser('u1')
  expect(archived.archived_at).toBe('2026-08-02T00:00:00Z')
})

test('unarchiveUser POSTs /users/{id}/unarchive', async () => {
  let hit = false
  server.use(
    http.post('/v1/users/u1/unarchive', () => {
      hit = true
      return HttpResponse.json(USER)
    }),
  )
  const restored = await unarchiveUser('u1')
  expect(hit).toBe(true)
  expect(restored.archived_at).toBeNull()
})

test('resetUserPassword POSTs email + new_password and tolerates a 204 with no body', async () => {
  let body: unknown
  server.use(
    http.post('/v1/users/password-reset', async ({ request }) => {
      body = await request.json()
      return new HttpResponse(null, { status: 204 })
    }),
  )
  await expect(resetUserPassword('a@b.co', 'password1')).resolves.toBeUndefined()
  expect(body).toEqual({ email: 'a@b.co', new_password: 'password1' })
})

test('a 409 from createUser surfaces as an ApiError with status 409', async () => {
  server.use(
    http.post('/v1/users', () =>
      HttpResponse.json({ error: 'email already registered' }, { status: 409 }),
    ),
  )
  await expect(
    createUser({ email: 'dupe@b.co', name: '', password: 'password1', is_admin: false }),
  ).rejects.toMatchObject({ status: 409, code: 'email already registered' })
})
