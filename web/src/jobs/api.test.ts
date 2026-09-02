import { expect, test, vi } from 'vitest'
import { http, HttpResponse } from 'msw'
import { server } from '../test/setup-helpers'
import { fakeSseServer, tick } from '../test/sseStream'
import { ApiError } from '../lib/api'
import {
  BACKFILL_PAGE_SIZE,
  cancelJob,
  createJob,
  getJobStats,
  getTaskLogs,
  getTaskLogsDesc,
  listJobs,
  listJobsBySchedule,
  retryJob,
  streamTaskLog,
  type JobsPage,
  type TaskLogEvent,
} from './api'

const emptyPage: JobsPage = { items: [], next_cursor: '', total: 0 }

test('unfiltered list sends sort + limit, no status', async () => {
  let captured: URLSearchParams | undefined
  server.use(
    http.get('/v1/jobs', ({ request }) => {
      captured = new URL(request.url).searchParams
      return HttpResponse.json(emptyPage)
    }),
  )
  await listJobs('name')
  expect(captured?.get('sort')).toBe('name')
  expect(captured?.get('limit')).toBe('50')
  expect(captured?.get('status')).toBeNull()
})

test('status filter omits sort (server 400s sort+status)', async () => {
  let captured: URLSearchParams | undefined
  server.use(
    http.get('/v1/jobs', ({ request }) => {
      captured = new URL(request.url).searchParams
      return HttpResponse.json(emptyPage)
    }),
  )
  await listJobs('name', 'running')
  expect(captured?.get('status')).toBe('running')
  expect(captured?.get('sort')).toBeNull()
})

test('passes cursor when provided', async () => {
  let captured: URLSearchParams | undefined
  server.use(
    http.get('/v1/jobs', ({ request }) => {
      captured = new URL(request.url).searchParams
      return HttpResponse.json(emptyPage)
    }),
  )
  await listJobs('-created_at', '', 'CUR')
  expect(captured?.get('cursor')).toBe('CUR')
})

test('getJobStats fetches /jobs/stats', async () => {
  server.use(
    http.get('/v1/jobs/stats', () =>
      HttpResponse.json({ running: 3, queued: 1, done_24h: 487, failed_24h: 12 }),
    ),
  )
  const stats = await getJobStats()
  expect(stats.running).toBe(3)
  expect(stats.done_24h).toBe(487)
})

test('throws ApiError on the error envelope', async () => {
  server.use(http.get('/v1/jobs', () => HttpResponse.json({ error: 'boom' }, { status: 500 })))
  await expect(listJobs('-created_at')).rejects.toBeInstanceOf(ApiError)
})

test('cancelJob DELETEs /jobs/{id} with no force param when force=false', async () => {
  let method: string | undefined
  let search: string | undefined
  server.use(
    http.delete('/v1/jobs/j1', ({ request }) => {
      method = request.method
      search = new URL(request.url).search
      return HttpResponse.json({ id: 'j1', status: 'cancelled' })
    }),
  )
  await cancelJob('j1', false)
  expect(method).toBe('DELETE')
  expect(search).toBe('')
})

test('cancelJob DELETEs /jobs/{id}?force=true when force=true', async () => {
  let search: string | null = null
  server.use(
    http.delete('/v1/jobs/j1', ({ request }) => {
      search = new URL(request.url).searchParams.get('force')
      return HttpResponse.json({ id: 'j1', status: 'cancelled' })
    }),
  )
  await cancelJob('j1', true)
  expect(search).toBe('true')
})

test('createJob POSTs the spec to /v1/jobs and returns the created job', async () => {
  let body: unknown = null
  let method = ''
  server.use(
    http.post('/v1/jobs', async ({ request }) => {
      method = request.method
      body = await request.json()
      return HttpResponse.json(
        { id: 'job-123', name: 'my-job', status: 'pending' },
        { status: 201 },
      )
    }),
  )

  const spec = { name: 'my-job', tasks: [{ name: 'hello', command: ['echo', 'hi'] }] }
  const job = await createJob(spec)

  expect(method).toBe('POST')
  expect(body).toEqual(spec)
  expect(job.id).toBe('job-123')
})

test('getTaskLogs sends limit=200 and omits since_seq on the first page', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/tasks/t1/logs', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [], next_seq: 0, prev_seq: 0, total: 0 })
    }),
  )
  await getTaskLogs('t1')
  expect(params?.get('limit')).toBe(String(BACKFILL_PAGE_SIZE))
  expect(BACKFILL_PAGE_SIZE).toBe(200) // the server's documented maximum
  expect(params?.has('since_seq')).toBe(false)
  // The forward reader must never acquire a direction: order=desc would make
  // this the newest page rather than the oldest, silently.
  expect(params?.has('order')).toBe(false)
})

test('getTaskLogs sends since_seq when paging forward', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/tasks/t1/logs', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [], next_seq: 0, prev_seq: 0, total: 7 })
    }),
  )
  const page = await getTaskLogs('t1', 41, 200)
  expect(params?.get('since_seq')).toBe('41')
  expect(page.total).toBe(7)
})

test('getTaskLogsDesc asks for the tail with no cursor, and for a page before one when given', async () => {
  let search = ''
  server.use(
    http.get('/v1/tasks/t1/logs', ({ request }) => {
      search = new URL(request.url).search
      return HttpResponse.json({ items: [], next_seq: 0, prev_seq: 0, total: 0 })
    }),
  )
  // The exact query string, not a subset: a leftover since_seq or a missing
  // order would still satisfy a per-key assertion.
  await getTaskLogsDesc('t1')
  expect(search).toBe('?order=desc&limit=200')

  await getTaskLogsDesc('t1', 93913)
  expect(search).toBe('?order=desc&limit=200&before_seq=93913')

  // 0 means "no cursor", and the server 400s before_seq=0, so it is never sent.
  await getTaskLogsDesc('t1', 0, 50)
  expect(search).toBe('?order=desc&limit=50')
})

test('getTaskLogsDesc reads prev_seq off the envelope', async () => {
  server.use(
    http.get('/v1/tasks/t1/logs', () =>
      // Hand-written, never marshalled through TaskLogPage: a fixture built
      // from the production type agrees with the decoder by construction.
      HttpResponse.json({
        items: [{ seq: 41, stream: 'stdout', content: 'x\n', created_at: '2026-09-01T00:00:00Z' }],
        next_seq: 0,
        prev_seq: 41,
        total: 94312,
      }),
    ),
  )
  const page = await getTaskLogsDesc('t1')
  expect(page.prev_seq).toBe(41)
  expect(page.total).toBe(94312)
})

test('streamTaskLog routes task_log frames to onLine and dropped frames to onDropped', async () => {
  const fake = fakeSseServer()
  const lines: TaskLogEvent[] = []
  const dropped = vi.fn()
  const p = streamTaskLog('t1', {
    signal: new AbortController().signal,
    onLine: (e) => lines.push(e),
    onDropped: dropped,
    fetchImpl: fake.fetchImpl,
  })
  const conn = await fake.waitForConnection()
  expect(conn.url).toBe('/v1/events?task_id=t1')

  conn.emit('task_log', { task_id: 't1', job_id: 'j1', seq: 9, stream: 'stderr', content: 'boom\n', created_at: '2026-08-09T00:00:00Z' })
  // A status frame can never reach a ?task_id=-only subscription
  // (README.md:1312-1313), and an unknown type is additive - both must be ignored
  // without throwing.
  conn.emit('task', { id: 't1', status: 'running' })
  conn.emit('brand_new', { x: 1 })
  conn.send('event: task_log\ndata: {not json}\n\n')
  await tick()
  expect(lines).toHaveLength(1)
  expect(lines[0]).toMatchObject({ seq: 9, stream: 'stderr', content: 'boom\n' })
  expect(dropped).not.toHaveBeenCalled()

  conn.emit('dropped', { reason: 'slow_consumer' })
  await tick()
  expect(dropped).toHaveBeenCalledTimes(1)

  conn.close()
  await p
})

test('retryJob POSTs /jobs/{id}/retry?task=failed with no request body', async () => {
  let search = ''
  let method = ''
  let body = ''
  server.use(
    http.post('/v1/jobs/j1/retry', async ({ request }) => {
      search = new URL(request.url).search
      method = request.method
      body = await request.text()
      return HttpResponse.json({ id: 'j1', status: 'running', tasks_retried: 3 })
    }),
  )
  const res = await retryJob('j1', 'failed')
  expect(method).toBe('POST')
  expect(search).toBe('?task=failed')
  // handleRetryJob never calls readJSON; a body would be a contract violation.
  expect(body).toBe('')
  expect(res.tasks_retried).toBe(3)
})

test('retryJob sends ?task=all for the all mode', async () => {
  let search = ''
  server.use(
    http.post('/v1/jobs/j1/retry', ({ request }) => {
      search = new URL(request.url).search
      return HttpResponse.json({ id: 'j1', status: 'running', tasks_retried: 9 })
    }),
  )
  await retryJob('j1', 'all')
  expect(search).toBe('?task=all')
})

test('listJobsBySchedule sends scheduled_job_id and limit and NEVER sends sort', async () => {
  let captured: URLSearchParams | undefined
  server.use(
    http.get('/v1/jobs', ({ request }) => {
      captured = new URL(request.url).searchParams
      return HttpResponse.json(emptyPage)
    }),
  )
  await listJobsBySchedule('sched-1', 20)
  // Presence controls FIRST: without them the absence assertion below passes
  // against a function that sends no parameters at all.
  expect(captured?.get('scheduled_job_id')).toBe('sched-1')
  expect(captured?.get('limit')).toBe('20')
  // The real failure this guards: ?sort= combined with ANY filter is a hard 400,
  // 'sort not supported on filtered list variant' (internal/api/jobs.go:417-422),
  // which the runs panel would render as a generic error box. listJobs sets sort by
  // default (api.ts:50-56), so a copy-paste reintroduces it.
  expect(captured?.has('sort')).toBe(false)
  expect(captured?.has('status')).toBe(false)
})
