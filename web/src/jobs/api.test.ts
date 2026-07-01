import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { ApiError } from '../lib/api'
import { listJobs, getJobStats, cancelJob, createJob, type JobsPage } from './api'

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
