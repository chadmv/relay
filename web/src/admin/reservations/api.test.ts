import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { server } from '../../test/setup-helpers'
import { ApiError } from '../../lib/api'
import { createReservation, deleteReservation, listReservations } from './api'

const ROW = {
  id: 'r1',
  name: 'gpu-farm-hold',
  selector: { tier: 'gpu' },
  worker_ids: ['11111111-1111-1111-1111-111111111111'],
  user_id: '99999999-9999-9999-9999-999999999999',
  created_at: '2026-08-09T09:30:00Z',
  project: 'atlas',
  starts_at: '2026-08-09T10:00:00Z',
  ends_at: '2026-08-11T10:00:00Z',
}

test('listReservations sends sort and limit=50, omits an empty cursor, returns the envelope', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/reservations', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [ROW], next_cursor: 'c2', total: 7 })
    }),
  )
  const page = await listReservations({ sort: '-created_at', cursor: '' })
  expect(params?.get('sort')).toBe('-created_at')
  // ?limit=0 is a 400 rather than a clamp (internal/api/pagination.go:244), so the
  // page size is always stated explicitly.
  expect(params?.get('limit')).toBe('50')
  expect(params?.has('cursor')).toBe(false)
  expect(page.items[0].name).toBe('gpu-farm-hold')
  expect(page.next_cursor).toBe('c2')
  expect(page.total).toBe(7)
})

test('listReservations sends the cursor and all EIGHT server sort values', async () => {
  const seen: string[] = []
  server.use(
    http.get('/v1/reservations', ({ request }) => {
      const p = new URL(request.url).searchParams
      seen.push(`${p.get('sort')}|${p.get('cursor')}`)
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  const sorts = [
    'created_at',
    '-created_at',
    'name',
    '-name',
    'starts_at',
    '-starts_at',
    'ends_at',
    '-ends_at',
  ] as const
  for (const sort of sorts) await listReservations({ sort, cursor: 'cur1' })
  expect(seen).toEqual(sorts.map((s) => `${s}|cur1`))
})

test('selector arrives as JSON null and as {} and as pairs - all three parse', async () => {
  server.use(
    http.get('/v1/reservations', () =>
      HttpResponse.json({
        items: [
          { ...ROW, id: 'r-null', selector: null },
          { ...ROW, id: 'r-empty', selector: {} },
          { ...ROW, id: 'r-pairs', selector: { tier: 'gpu', site: 'west' } },
        ],
        next_cursor: '',
        total: 3,
      }),
    ),
  )
  const page = await listReservations({ sort: '-created_at', cursor: '' })
  // `selector` has NO omitempty and goes through rawJSON, not rawObject
  // (internal/api/reservations.go:16, :45; internal/api/server.go:236-240), so a
  // create with no selector marshals a nil map to the literal `null` and the key is
  // PRESENT with value null. Column-default rows read {}. The type is
  // `Record<string, string> | null` and must tolerate both.
  expect(page.items[0].selector).toBeNull()
  expect(page.items[1].selector).toEqual({})
  expect(page.items[2].selector).toEqual({ tier: 'gpu', site: 'west' })
})

test('project / starts_at / ends_at keys are ABSENT (not null) when the column is NULL', async () => {
  server.use(
    http.get('/v1/reservations', () =>
      HttpResponse.json({
        items: [
          {
            id: 'r2',
            name: 'open-ended',
            selector: null,
            worker_ids: [],
            user_id: ROW.user_id,
            created_at: ROW.created_at,
          },
        ],
        next_cursor: '',
        total: 1,
      }),
    ),
  )
  const page = await listReservations({ sort: '-created_at', cursor: '' })
  const r = page.items[0]
  // omitempty on a POINTER field omits the key entirely
  // (internal/api/reservations.go:19-21), so these are `?: string`, never
  // `string | null`, and consumers must handle undefined - not null.
  for (const key of ['project', 'starts_at', 'ends_at']) {
    expect(key in r).toBe(false)
  }
  expect(r.project).toBeUndefined()
  // Positive control on the same instrument: a key that IS always present.
  expect('created_at' in r).toBe(true)
  // worker_ids is built with make(), so it is [] and never null.
  expect(r.worker_ids).toEqual([])
})

test('createReservation sends the exact body, as JSON, with NO selector and NO user_id', async () => {
  let body: Record<string, unknown> | undefined
  let contentType: string | null = null
  server.use(
    http.post('/v1/reservations', async ({ request }) => {
      // Mirrors readJSON (internal/api/server.go:196-211): an absent or
      // unparseable body is a 400 'invalid request body'. That is what makes this
      // non-vacuous - a client that stopped sending a body would fail here exactly
      // as it does against the real server.
      const raw = await request.text()
      if (raw === '') return HttpResponse.json({ error: 'invalid request body' }, { status: 400 })
      body = JSON.parse(raw)
      contentType = request.headers.get('content-type')
      return HttpResponse.json({ ...ROW, id: 'r9' }, { status: 201 })
    }),
  )
  const created = await createReservation({
    name: 'gpu-farm-hold',
    worker_ids: [ROW.worker_ids[0]],
    project: 'atlas',
    starts_at: '2026-08-09T10:00:00.000Z',
  })
  expect(body).toEqual({
    name: 'gpu-farm-hold',
    worker_ids: [ROW.worker_ids[0]],
    project: 'atlas',
    starts_at: '2026-08-09T10:00:00.000Z',
  })
  // Asserted on the PARSED body, not a substring: `selector` is never sent because
  // the scheduler never reads it, and `user_id` is never sent because the handler
  // defaults it to the authenticated admin and a valid-but-nonexistent UUID would
  // produce a 500 from the users FK (internal/api/reservations.go:255-263, :294-297).
  expect('selector' in body!).toBe(false)
  expect('user_id' in body!).toBe(false)
  expect(contentType).toContain('application/json')
  expect(created.id).toBe('r9')
})

test('createReservation omits blank optionals rather than sending empty strings', async () => {
  let body: Record<string, unknown> | undefined
  server.use(
    http.post('/v1/reservations', async ({ request }) => {
      body = (await request.json()) as Record<string, unknown>
      return HttpResponse.json({ ...ROW, id: 'r9' }, { status: 201 })
    }),
  )
  await createReservation({ name: 'minimal', worker_ids: [ROW.worker_ids[0]] })
  expect(body).toEqual({ name: 'minimal', worker_ids: [ROW.worker_ids[0]] })
  for (const key of ['project', 'starts_at', 'ends_at']) {
    expect(key in body!).toBe(false)
  }
})

test('a create 500 surfaces as an ApiError with the server message', async () => {
  // Reachable in production only via a valid-but-nonexistent user_id, which this UI
  // never sends. Kept because the error path must still render, not crash.
  server.use(
    http.post('/v1/reservations', () =>
      HttpResponse.json({ error: 'create reservation failed' }, { status: 500 }),
    ),
  )
  await expect(createReservation({ name: 'x', worker_ids: [] })).rejects.toMatchObject({
    status: 500,
    code: 'create reservation failed',
  })
})

test('deleteReservation issues DELETE on the id path and tolerates a 204 with NO body', async () => {
  let method: string | undefined
  let path: string | undefined
  server.use(
    http.delete('/v1/reservations/:id', ({ request }) => {
      method = request.method
      path = new URL(request.url).pathname
      // A real 204 has no body at all. A client that unconditionally calls
      // res.json() throws 'Unexpected end of JSON input' here.
      return new HttpResponse(null, { status: 204 })
    }),
  )
  await expect(deleteReservation('r1')).resolves.toBeUndefined()
  expect(method).toBe('DELETE')
  expect(path).toBe('/v1/reservations/r1')
})

test('a delete 404 surfaces as ApiError(404, "reservation not found")', async () => {
  server.use(
    http.delete('/v1/reservations/:id', () =>
      HttpResponse.json({ error: 'reservation not found' }, { status: 404 }),
    ),
  )
  const err = await deleteReservation('gone').catch((e) => e)
  expect(err).toBeInstanceOf(ApiError)
  expect(err).toMatchObject({ status: 404, code: 'reservation not found' })
})
