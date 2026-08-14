import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { ApiError } from '../lib/api'
import {
  deleteSchedule,
  getSchedule,
  listSchedules,
  runScheduleNow,
  setScheduleEnabled,
  updateSchedule,
  type SchedulesPage,
} from './api'

const emptyPage: SchedulesPage = { items: [], next_cursor: '', total: 0 }

test('listSchedules requests the first page with sort and limit=50, no cursor', async () => {
  let captured: URLSearchParams | undefined
  server.use(
    http.get('/v1/scheduled-jobs', ({ request }) => {
      captured = new URL(request.url).searchParams
      return HttpResponse.json(emptyPage)
    }),
  )
  await listSchedules('name')
  expect(captured?.get('sort')).toBe('name')
  expect(captured?.get('limit')).toBe('50')
  expect(captured?.get('cursor')).toBeNull()
})

test('listSchedules includes the cursor when provided', async () => {
  let captured: URLSearchParams | undefined
  server.use(
    http.get('/v1/scheduled-jobs', ({ request }) => {
      captured = new URL(request.url).searchParams
      return HttpResponse.json(emptyPage)
    }),
  )
  await listSchedules('-created_at', 'CUR123')
  expect(captured?.get('cursor')).toBe('CUR123')
})

test('listSchedules parses the page payload', async () => {
  server.use(
    http.get('/v1/scheduled-jobs', () =>
      HttpResponse.json({
        items: [{ id: 's1', name: 'nightly', owner_email: 'a@b.com', enabled: true }],
        next_cursor: 'abc',
        total: 1,
      }),
    ),
  )
  const page = await listSchedules('-created_at')
  expect(page.total).toBe(1)
  expect(page.items[0].name).toBe('nightly')
})

test('listSchedules throws ApiError on the error envelope', async () => {
  server.use(
    http.get('/v1/scheduled-jobs', () => HttpResponse.json({ error: 'boom' }, { status: 500 })),
  )
  await expect(listSchedules('-created_at')).rejects.toBeInstanceOf(ApiError)
})

test('runScheduleNow POSTs to the run-now path', async () => {
  let method: string | undefined
  let path: string | undefined
  server.use(
    http.post('/v1/scheduled-jobs/s1/run-now', ({ request }) => {
      method = request.method
      path = new URL(request.url).pathname
      return HttpResponse.json({ id: 'job1' }, { status: 201 })
    }),
  )
  await runScheduleNow('s1')
  expect(method).toBe('POST')
  expect(path).toBe('/v1/scheduled-jobs/s1/run-now')
})

test('setScheduleEnabled PATCHes the enabled flag', async () => {
  let body: unknown
  server.use(
    http.patch('/v1/scheduled-jobs/s1', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ id: 's1', enabled: false })
    }),
  )
  await setScheduleEnabled('s1', false)
  expect(body).toEqual({ enabled: false })
})

const ROW = {
  id: 's1',
  name: 'nightly-build',
  owner_id: 'o1',
  owner_email: 'dev@studio.com',
  cron_expr: '0 2 * * *',
  timezone: 'UTC',
  job_spec: { name: 'nightly-build', tasks: [] },
  overlap_policy: 'skip',
  enabled: true,
  next_run_at: '2099-01-01T00:00:00Z',
  created_at: '2026-06-01T00:00:00Z',
  updated_at: '2026-06-05T11:00:00Z',
}

test('getSchedule GETs the id path and parses the row', async () => {
  let path: string | undefined
  server.use(
    http.get('/v1/scheduled-jobs/s1', ({ request }) => {
      path = new URL(request.url).pathname
      return HttpResponse.json(ROW)
    }),
  )
  const s = await getSchedule('s1')
  expect(path).toBe('/v1/scheduled-jobs/s1')
  expect(s.name).toBe('nightly-build')
  expect(s.cron_expr).toBe('0 2 * * *')
  // Populated on THIS endpoint too: handleGetScheduledJob goes through
  // fillOwnerEmails, same as both list arms. OwnerEmail has no omitempty, so the
  // key is always present; this asserts the client parses the value through.
  expect(s.owner_email).toBe('dev@studio.com')
  // last_run_at / last_job_id carry omitempty, so the KEY IS ABSENT when NULL -
  // not null. Consumers must handle undefined, never `=== null`.
  expect('last_run_at' in s).toBe(false)
  expect('last_job_id' in s).toBe(false)
  // Positive control on the same instrument: a key that is always present, so the
  // two absence assertions above are about omitempty and not about a dead `in`.
  expect('next_run_at' in s).toBe(true)
})

test('getSchedule surfaces the owner-or-admin deny as ApiError(404)', async () => {
  // ownedScheduledJob 404s a non-owner non-admin exactly as it 404s a missing row
  // (internal/api/scheduled_jobs.go:147-169): the resource is hidden, not refused.
  // The client cannot and must not try to distinguish the two.
  server.use(
    http.get('/v1/scheduled-jobs/nope', () =>
      HttpResponse.json({ error: 'scheduled job not found' }, { status: 404 }),
    ),
  )
  const err = await getSchedule('nope').catch((e) => e)
  expect(err).toBeInstanceOf(ApiError)
  expect(err).toMatchObject({ status: 404, code: 'scheduled job not found' })
})

test('updateSchedule PATCHes exactly the keys it is given and NO others', async () => {
  let body: Record<string, unknown> | undefined
  let method: string | undefined
  server.use(
    http.patch('/v1/scheduled-jobs/s1', async ({ request }) => {
      method = request.method
      body = (await request.json()) as Record<string, unknown>
      return HttpResponse.json({ ...ROW, timezone: 'Europe/Berlin' })
    }),
  )
  await updateSchedule('s1', { timezone: 'Europe/Berlin' })
  expect(method).toBe('PATCH')
  expect(body).toEqual({ timezone: 'Europe/Berlin' })
  // The absence assertion that matters: a body CARRYING cron_expr recomputes
  // next_run_at from time.Now() even when the value is unchanged
  // (internal/api/scheduled_jobs.go:585, :595).
  expect('cron_expr' in body!).toBe(false)
  expect('enabled' in body!).toBe(false)
})

test('updateSchedule passes cron_expr through when it IS given (positive control)', async () => {
  // Without this, the previous test passes against a client that always sends {}.
  let body: Record<string, unknown> | undefined
  server.use(
    http.patch('/v1/scheduled-jobs/s1', async ({ request }) => {
      body = (await request.json()) as Record<string, unknown>
      return HttpResponse.json(ROW)
    }),
  )
  await updateSchedule('s1', { cron_expr: '@every 30m', overlap_policy: 'allow' })
  expect(body).toEqual({ cron_expr: '@every 30m', overlap_policy: 'allow' })
})

test('updateSchedule surfaces the server 400 message verbatim', async () => {
  // There is no client-side cron validation by design, so this message is the
  // ONLY feedback a bad cron produces. It comes from internal/schedrunner/cron.go:39.
  server.use(
    http.patch('/v1/scheduled-jobs/s1', () =>
      HttpResponse.json(
        { error: 'invalid cron expression "nope": expected exactly 5 fields, found 1: [nope]' },
        { status: 400 },
      ),
    ),
  )
  const err = await updateSchedule('s1', { cron_expr: 'nope' }).catch((e) => e)
  expect(err).toBeInstanceOf(ApiError)
  expect(err.status).toBe(400)
  expect(err.message).toContain('invalid cron expression')
})

test('deleteSchedule DELETEs the id path and tolerates the empty 204', async () => {
  let method: string | undefined
  let path: string | undefined
  server.use(
    http.delete('/v1/scheduled-jobs/s1', ({ request }) => {
      method = request.method
      path = new URL(request.url).pathname
      // A real 204 has NO body at all (internal/api/scheduled_jobs.go:633). A client
      // that unconditionally calls res.json() throws 'Unexpected end of JSON input'.
      return new HttpResponse(null, { status: 204 })
    }),
  )
  await expect(deleteSchedule('s1')).resolves.toBeUndefined()
  expect(method).toBe('DELETE')
  expect(path).toBe('/v1/scheduled-jobs/s1')
})

// fetch() normalizes ".." path segments before the request is dispatched, so an
// unencoded id like "../jobs/<uuid>" turns e.g. the Delete call into a request
// against a DIFFERENT resource entirely - DELETE /v1/jobs/<uuid> instead of
// /v1/scheduled-jobs/<id>. encodeURIComponent turns the traversal segment's
// slashes into %2F, which a URL parser does NOT treat as path separators, so the
// request stays scoped under /v1/scheduled-jobs/ no matter what the id contains.
const TRAVERSAL_ID = '../jobs/deadbeef-0000-0000-0000-000000000000'
const TRAVERSAL_TARGET = '/v1/jobs/deadbeef-0000-0000-0000-000000000000'
const TRAVERSAL_ENCODED = `/v1/scheduled-jobs/${encodeURIComponent(TRAVERSAL_ID)}`

test('runScheduleNow encodes the id so a path-traversal id cannot escape /scheduled-jobs', async () => {
  let capturedPath: string | undefined
  server.use(
    http.post('*', ({ request }) => {
      capturedPath = new URL(request.url).pathname
      return HttpResponse.json({ id: 'job1' }, { status: 201 })
    }),
  )
  await runScheduleNow(TRAVERSAL_ID)
  expect(capturedPath).not.toBe(TRAVERSAL_TARGET + '/run-now')
  expect(capturedPath).toBe(TRAVERSAL_ENCODED + '/run-now')
})

test('getSchedule encodes the id so a path-traversal id cannot escape /scheduled-jobs', async () => {
  let capturedPath: string | undefined
  server.use(
    http.get('*', ({ request }) => {
      capturedPath = new URL(request.url).pathname
      return HttpResponse.json(ROW)
    }),
  )
  await getSchedule(TRAVERSAL_ID)
  expect(capturedPath).not.toBe(TRAVERSAL_TARGET)
  expect(capturedPath).toBe(TRAVERSAL_ENCODED)
})

test('updateSchedule encodes the id so a path-traversal id cannot escape /scheduled-jobs', async () => {
  let capturedPath: string | undefined
  server.use(
    http.patch('*', ({ request }) => {
      capturedPath = new URL(request.url).pathname
      return HttpResponse.json(ROW)
    }),
  )
  await updateSchedule(TRAVERSAL_ID, { overlap_policy: 'allow' })
  expect(capturedPath).not.toBe(TRAVERSAL_TARGET)
  expect(capturedPath).toBe(TRAVERSAL_ENCODED)
})

test('deleteSchedule encodes the id so a path-traversal id cannot escape /scheduled-jobs', async () => {
  let capturedPath: string | undefined
  server.use(
    http.delete('*', ({ request }) => {
      capturedPath = new URL(request.url).pathname
      return new HttpResponse(null, { status: 204 })
    }),
  )
  await deleteSchedule(TRAVERSAL_ID)
  expect(capturedPath).not.toBe(TRAVERSAL_TARGET)
  expect(capturedPath).toBe(TRAVERSAL_ENCODED)
})

test('setScheduleEnabled still sends EXACTLY { enabled } after the re-expression', async () => {
  let body: Record<string, unknown> | undefined
  server.use(
    http.patch('/v1/scheduled-jobs/s1', async ({ request }) => {
      body = (await request.json()) as Record<string, unknown>
      return HttpResponse.json({ ...ROW, enabled: false })
    }),
  )
  await setScheduleEnabled('s1', false)
  // Routing this through updateSchedule must not smuggle extra keys in: a
  // cron_expr or timezone here would recompute next_run_at on every single
  // Enable/Disable click (internal/api/scheduled_jobs.go:585).
  expect(body).toEqual({ enabled: false })
})
