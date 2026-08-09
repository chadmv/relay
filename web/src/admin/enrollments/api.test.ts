import { expect, test } from 'vitest'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/setup-helpers'
import {
  createAgentEnrollment,
  listAgentEnrollments,
  DEFAULT_TTL_SECONDS,
  MAX_TTL_SECONDS,
  MIN_TTL_SECONDS,
  TTL_PRESETS,
} from './api'

const ROW = {
  id: 'e1',
  created_at: '2026-08-09T00:00:00Z',
  expires_at: '2026-08-10T00:00:00Z',
  created_by: '11111111-2222-3333-4444-555555555555',
  hostname_hint: 'farm-west-13',
}

test('listAgentEnrollments sends sort and limit=50, omits an empty cursor, and returns the envelope', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/agent-enrollments', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [ROW], next_cursor: 'c2', total: 7 })
    }),
  )
  const page = await listAgentEnrollments({ sort: '-created_at', cursor: '' })
  expect(params?.get('sort')).toBe('-created_at')
  // ?limit=0 is a 400 rather than a clamp (internal/api/pagination.go:244), so the
  // page size is always stated explicitly.
  expect(params?.get('limit')).toBe('50')
  expect(params?.has('cursor')).toBe(false)
  expect(page.items[0].hostname_hint).toBe('farm-west-13')
  expect(page.next_cursor).toBe('c2')
  expect(page.total).toBe(7)
})

test('listAgentEnrollments sends the cursor and each of the four sort values', async () => {
  const seen: string[] = []
  server.use(
    http.get('/v1/agent-enrollments', ({ request }) => {
      const p = new URL(request.url).searchParams
      seen.push(`${p.get('sort')}|${p.get('cursor')}`)
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  for (const sort of ['created_at', '-created_at', 'expires_at', '-expires_at'] as const) {
    await listAgentEnrollments({ sort, cursor: 'cur1' })
  }
  expect(seen).toEqual([
    'created_at|cur1',
    '-created_at|cur1',
    'expires_at|cur1',
    '-expires_at|cur1',
  ])
})

test('a row with no hostname_hint keeps the key ABSENT, not null', async () => {
  server.use(
    http.get('/v1/agent-enrollments', () =>
      HttpResponse.json({
        items: [{ id: 'e2', created_at: ROW.created_at, expires_at: ROW.expires_at, created_by: ROW.created_by }],
        next_cursor: '',
        total: 1,
      }),
    ),
  )
  const page = await listAgentEnrollments({ sort: '-created_at', cursor: '' })
  // enrollmentRowToMap omits the key entirely when the column is NULL
  // (internal/api/agent_enrollments.go:90-92), so the type is `?: string`, never
  // `string | null`, and consumers must handle undefined - not null.
  expect('hostname_hint' in page.items[0]).toBe(false)
  expect(page.items[0].hostname_hint).toBeUndefined()
})

test('createAgentEnrollment ALWAYS sends a JSON body, and the 201 parses', async () => {
  let body: unknown
  let contentType: string | null = null
  server.use(
    http.post('/v1/agent-enrollments', async ({ request }) => {
      // This handler mirrors readJSON (internal/api/server.go:199-211): an absent
      // or unparseable body is a 400 "invalid request body". That is what makes
      // this test non-vacuous - if the client ever stops sending a body, the
      // request fails here exactly as it would against the real server.
      const raw = await request.text()
      if (raw === '') return HttpResponse.json({ error: 'invalid request body' }, { status: 400 })
      try {
        body = JSON.parse(raw)
      } catch {
        return HttpResponse.json({ error: 'invalid request body' }, { status: 400 })
      }
      contentType = request.headers.get('content-type')
      return HttpResponse.json(
        { id: 'e9', token: 'f00dcafe'.repeat(8), expires_at: '2026-08-10T00:00:00Z' },
        { status: 201 },
      )
    }),
  )
  const created = await createAgentEnrollment({ ttl_seconds: DEFAULT_TTL_SECONDS })
  // The exact preset literal, never 0 and never absent: relying on the server's
  // zero-means-default branch (internal/api/agent_enrollments.go:32-34) would hide
  // the real TTL from the request log and from this assertion.
  expect(body).toEqual({ ttl_seconds: 86400 })
  expect(contentType).toContain('application/json')
  expect(created).toEqual({ id: 'e9', token: 'f00dcafe'.repeat(8), expires_at: '2026-08-10T00:00:00Z' })
})

test('createAgentEnrollment includes hostname_hint only when it is provided', async () => {
  let body: unknown
  server.use(
    http.post('/v1/agent-enrollments', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ id: 'e9', token: 'tok', expires_at: ROW.expires_at }, { status: 201 })
    }),
  )
  await createAgentEnrollment({ hostname_hint: 'farm-west-13', ttl_seconds: 3600 })
  expect(body).toEqual({ hostname_hint: 'farm-west-13', ttl_seconds: 3600 })
})

test('a 400 surfaces as an ApiError carrying the status and the server message', async () => {
  server.use(
    http.post('/v1/agent-enrollments', () =>
      HttpResponse.json({ error: 'ttl_seconds must be at least 60' }, { status: 400 }),
    ),
  )
  await expect(createAgentEnrollment({ ttl_seconds: 30 })).rejects.toMatchObject({
    status: 400,
    code: 'ttl_seconds must be at least 60',
  })
})

// This is the client-side validation of the server's TTL bounds. Because the UI
// offers presets only, there is no runtime input to validate - so the bounds are
// enforced structurally here instead: a future preset (or a free-form field that
// reuses these constants) cannot silently exceed what the server accepts.
test('every TTL preset is within the server bounds and the default is one of them', () => {
  expect(MIN_TTL_SECONDS).toBe(60)
  expect(MAX_TTL_SECONDS).toBe(604800)
  expect(DEFAULT_TTL_SECONDS).toBe(86400)
  expect(TTL_PRESETS.map((p) => p.label)).toEqual(['1h', '24h', '3d', '7d'])
  expect(TTL_PRESETS.map((p) => p.seconds)).toEqual([3600, 86400, 259200, 604800])
  for (const p of TTL_PRESETS) {
    expect(p.seconds).toBeGreaterThanOrEqual(MIN_TTL_SECONDS)
    expect(p.seconds).toBeLessThanOrEqual(MAX_TTL_SECONDS)
  }
  expect(TTL_PRESETS.some((p) => p.seconds === DEFAULT_TTL_SECONDS)).toBe(true)
})
