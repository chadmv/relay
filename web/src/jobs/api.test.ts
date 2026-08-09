import { expect, test, vi } from 'vitest'
import { http, HttpResponse } from 'msw'
import { server } from '../test/setup-helpers'
import { fakeSseServer, tick } from '../test/sseStream'
import { BACKFILL_PAGE_SIZE, getTaskLogs, streamTaskLog, type TaskLogEvent } from './api'
import { isTerminalTask } from './taskStatus'

test('getTaskLogs sends limit=200 and omits since_seq on the first page', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/tasks/t1/logs', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [], next_seq: 0, total: 0 })
    }),
  )
  await getTaskLogs('t1')
  expect(params?.get('limit')).toBe(String(BACKFILL_PAGE_SIZE))
  expect(BACKFILL_PAGE_SIZE).toBe(200) // the server's documented maximum
  expect(params?.has('since_seq')).toBe(false)
})

test('getTaskLogs sends since_seq when paging forward', async () => {
  let params: URLSearchParams | undefined
  server.use(
    http.get('/v1/tasks/t1/logs', ({ request }) => {
      params = new URL(request.url).searchParams
      return HttpResponse.json({ items: [], next_seq: 0, total: 7 })
    }),
  )
  const page = await getTaskLogs('t1', 41, 200)
  expect(params?.get('since_seq')).toBe('41')
  expect(page.total).toBe(7)
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

test('isTerminalTask covers exactly done, failed and timed_out', () => {
  expect(isTerminalTask('done')).toBe(true)
  expect(isTerminalTask('failed')).toBe(true)
  expect(isTerminalTask('timed_out')).toBe(true)
  expect(isTerminalTask('pending')).toBe(false)
  expect(isTerminalTask('dispatched')).toBe(false)
  expect(isTerminalTask('running')).toBe(false)
  expect(isTerminalTask(undefined)).toBe(false)
})
