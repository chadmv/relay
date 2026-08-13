import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { server } from '../../test/setup-helpers'
import {
  createInvite,
  listInvites,
  DEFAULT_EXPIRES_IN,
  MAX_EXPIRES_IN_HOURS,
  TTL_PRESETS,
} from './api'

const ROW = {
  id: 'i1',
  created_at: '2026-08-01T09:00:00Z',
  expires_at: '2026-08-04T09:00:00Z',
  created_by: '11111111-2222-3333-4444-555555555555',
  created_by_email: 'admin@studio.dev',
  email: 'invitee@studio.dev',
}

test('listInvites sends sort and limit=50, omits an empty cursor, and returns the envelope', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/invites', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [ROW], next_cursor: 'c2', total: 7 })
    }),
  )
  const page = await listInvites({ sort: '-created_at', cursor: '' })
  expect(params?.get('sort')).toBe('-created_at')
  // ?limit=0 is a 400 rather than a clamp (internal/api/pagination.go:244), so the
  // page size is always stated explicitly.
  expect(params?.get('limit')).toBe('50')
  expect(params?.has('cursor')).toBe(false)
  expect(page.items[0].created_by_email).toBe('admin@studio.dev')
  expect(page.next_cursor).toBe('c2')
  expect(page.total).toBe(7)
})

test('listInvites sends the cursor and each of the four sort values', async () => {
  const seen: string[] = []
  server.use(
    http.get('/v1/invites', ({ request }) => {
      const p = new URL(request.url).searchParams
      seen.push(`${p.get('sort')}|${p.get('cursor')}`)
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  for (const sort of ['created_at', '-created_at', 'expires_at', '-expires_at'] as const) {
    await listInvites({ sort, cursor: 'cur1' })
  }
  expect(seen).toEqual([
    'created_at|cur1',
    '-created_at|cur1',
    'expires_at|cur1',
    '-expires_at|cur1',
  ])
})

test('email and used_at stay ABSENT rather than null when the server omits them', async () => {
  server.use(
    http.get('/v1/invites', () =>
      HttpResponse.json({
        items: [
          {
            id: 'i2',
            created_at: ROW.created_at,
            expires_at: ROW.expires_at,
            created_by: ROW.created_by,
            created_by_email: ROW.created_by_email,
          },
        ],
        next_cursor: '',
        total: 1,
      }),
    ),
  )
  const page = await listInvites({ sort: '-created_at', cursor: '' })
  // inviteEntry adds these two keys conditionally (internal/api/invites.go:139-144),
  // so the types are `?: string`, never `string | null`, and every consumer must
  // test for undefined - a `!== null` check would be always-true.
  expect('email' in page.items[0]).toBe(false)
  expect('used_at' in page.items[0]).toBe(false)
  expect(page.items[0].email).toBeUndefined()
  expect(page.items[0].used_at).toBeUndefined()
})

test('createInvite ALWAYS sends a JSON body, even with no email', async () => {
  let body: unknown
  let contentType: string | null = null
  server.use(
    http.post('/v1/invites', async ({ request }) => {
      // This handler mirrors readJSON (internal/api/server.go:199-211): an absent or
      // unparseable body is a 400 "invalid request body". readJSON runs
      // UNCONDITIONALLY on this endpoint (internal/api/invites.go:27), so that is
      // what makes this test non-vacuous - a client that stops sending a body fails
      // here exactly as it would against the real server.
      const raw = await request.text()
      if (raw === '') return HttpResponse.json({ error: 'invalid request body' }, { status: 400 })
      try {
        body = JSON.parse(raw)
      } catch {
        return HttpResponse.json({ error: 'invalid request body' }, { status: 400 })
      }
      contentType = request.headers.get('content-type')
      return HttpResponse.json(
        { id: 'i9', token: 'f00dcafe'.repeat(8), expires_at: ROW.expires_at },
        { status: 201 },
      )
    }),
  )
  const created = await createInvite({ expires_in: DEFAULT_EXPIRES_IN })
  expect(body).toEqual({ expires_in: '72h' })
  expect(contentType).toContain('application/json')
  expect(created.token).toBe('f00dcafe'.repeat(8))
})

test('createInvite sends the email key only when one is supplied', async () => {
  let body: unknown
  server.use(
    http.post('/v1/invites', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json(
        { id: 'i9', token: 'tok', expires_at: ROW.expires_at, email: 'invitee@studio.dev' },
        { status: 201 },
      )
    }),
  )
  const created = await createInvite({ email: 'invitee@studio.dev', expires_in: '24h' })
  expect(body).toEqual({ email: 'invitee@studio.dev', expires_in: '24h' })
  expect(created.email).toBe('invitee@studio.dev')
})

test('a 400 surfaces as an ApiError carrying the status and the server message', async () => {
  server.use(
    http.post('/v1/invites', () =>
      HttpResponse.json({ error: 'invalid email address' }, { status: 400 }),
    ),
  )
  await expect(createInvite({ email: 'nope', expires_in: '72h' })).rejects.toMatchObject({
    status: 400,
    code: 'invalid email address',
  })
})

// THE discriminating test of this file. `expires_in` is parsed by Go's
// time.ParseDuration (internal/api/invites.go:34), which accepts h/m/s and
// smaller and has NO DAY UNIT: ParseDuration("7d") returns `unknown unit "d"`,
// so a preset shipped as "7d" 400s in production while passing any naive
// "there are four presets" test. The labels stay human ("30d" is readable,
// "720h" is hostile); only the WIRE VALUES are constrained.
test('every TTL preset is hour-denominated and inside the server bounds', () => {
  expect(TTL_PRESETS.map((p) => p.label)).toEqual(['24h', '72h', '7d', '30d'])
  expect(TTL_PRESETS.map((p) => p.value)).toEqual(['24h', '72h', '168h', '720h'])
  for (const p of TTL_PRESETS) {
    expect(p.value).toMatch(/^\d+h$/)
    // Explicitly, not just as a consequence of the regex: a day suffix is the
    // exact failure this test exists to catch.
    expect(p.value).not.toMatch(/[dwy]$/)
    const hours = Number(p.value.slice(0, -1))
    expect(hours).toBeGreaterThan(0)
    // 720h EXACTLY is accepted: the server rejects `dur > maxInviteDuration`
    // (internal/api/invites.go:44), not `>=`.
    expect(hours).toBeLessThanOrEqual(MAX_EXPIRES_IN_HOURS)
  }
  expect(MAX_EXPIRES_IN_HOURS).toBe(720)
})

test('the default preset is 72h, matching the server default, and is one of the four', () => {
  expect(DEFAULT_EXPIRES_IN).toBe('72h')
  expect(TTL_PRESETS.some((p) => p.value === DEFAULT_EXPIRES_IN)).toBe(true)
})

test('two presets deliberately show a label that is NOT their wire value', () => {
  // Pins the divergence as intentional. If someone "simplifies" this by making
  // label === value, either the UI starts showing 720h or the wire starts
  // carrying 30d - and the second one 400s.
  expect(TTL_PRESETS.filter((p) => p.label !== p.value).map((p) => p.label)).toEqual(['7d', '30d'])
})
