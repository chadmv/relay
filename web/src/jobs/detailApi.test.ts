import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { ApiError } from '../lib/api'
import { getJob, getTaskLogs } from './api'

test('getJob fetches /jobs/:id and returns the detail with tasks', async () => {
  server.use(
    http.get('/v1/jobs/j1', () =>
      HttpResponse.json({
        id: 'j1',
        name: 'render',
        priority: 'high',
        status: 'running',
        submitted_by: 'u1',
        submitted_by_email: 'mira@studio.dev',
        labels: { team: 'fx' },
        created_at: '2026-07-01T00:00:00Z',
        updated_at: '2026-07-01T00:01:00Z',
        tasks: [
          {
            id: 't1',
            name: 'frame-001',
            status: 'done',
            commands: [['blender', '-b']],
            env: {},
            requires: {},
            timeout_seconds: 3600,
            retries: 2,
            retry_count: 0,
          },
        ],
      }),
    ),
  )
  const job = await getJob('j1')
  expect(job.name).toBe('render')
  expect(job.tasks[0].name).toBe('frame-001')
  expect(job.tasks[0].status).toBe('done')
})

test('getTaskLogs fetches /tasks/:id/logs with no since_seq by default', async () => {
  let captured: URLSearchParams | undefined
  server.use(
    http.get('/v1/tasks/t1/logs', ({ request }) => {
      captured = new URL(request.url).searchParams
      return HttpResponse.json({ items: [], next_seq: 0, total: 0 })
    }),
  )
  await getTaskLogs('t1')
  expect(captured?.get('since_seq')).toBeNull()
})

test('getTaskLogs passes since_seq when provided', async () => {
  let captured: URLSearchParams | undefined
  server.use(
    http.get('/v1/tasks/t1/logs', ({ request }) => {
      captured = new URL(request.url).searchParams
      return HttpResponse.json({ items: [], next_seq: 0, total: 0 })
    }),
  )
  await getTaskLogs('t1', 42)
  expect(captured?.get('since_seq')).toBe('42')
})

test('getJob throws ApiError on the error envelope', async () => {
  server.use(http.get('/v1/jobs/nope', () => HttpResponse.json({ error: 'job not found' }, { status: 404 })))
  await expect(getJob('nope')).rejects.toBeInstanceOf(ApiError)
})
