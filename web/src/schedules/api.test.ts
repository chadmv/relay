import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { ApiError } from '../lib/api'
import { listSchedules, runScheduleNow, setScheduleEnabled, type SchedulesPage } from './api'

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
