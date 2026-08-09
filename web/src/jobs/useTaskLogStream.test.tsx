import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { fakeSseServer, tick } from '../test/sseStream'
import { MAX_BACKFILL_PAGES, useTaskLogStream } from './useTaskLogStream'

function entry(seq: number, content = `line-${seq}\n`) {
  return { seq, stream: 'stdout' as const, content, created_at: '2026-08-09T00:00:00Z' }
}
function logEvent(seq: number, content = `line-${seq}\n`) {
  return { task_id: 't1', job_id: 'j1', ...entry(seq, content) }
}

// The ordering guard. Both events must be recorded, so a run that made only one
// request cannot pass. Prove it RED by swapping the two statements in the hook.
test('subscribes to the stream BEFORE it requests the first history page', async () => {
  const fake = fakeSseServer()
  const order: string[] = []
  server.use(
    http.get('/v1/tasks/t1/logs', () => {
      order.push('logs')
      return HttpResponse.json({ items: [entry(1)], next_seq: 0, total: 1 })
    }),
  )
  const wrapped = ((input: RequestInfo | URL, init?: RequestInit) => {
    order.push('stream')
    return fake.fetchImpl(input, init)
  }) as typeof fetch

  const { result } = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: wrapped }),
  )
  await waitFor(() => expect(result.current.status).toBe('live'))
  expect(order).toEqual(['stream', 'logs'])
})

test('applies frames buffered during backfill, deduping any also present in a page', async () => {
  const fake = fakeSseServer()
  let release: () => void = () => {}
  const gate = new Promise<void>((r) => {
    release = r
  })
  server.use(
    http.get('/v1/tasks/t1/logs', async () => {
      await gate
      return HttpResponse.json({ items: [entry(1), entry(2)], next_seq: 0, total: 2 })
    }),
  )
  const { result } = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  const conn = await fake.waitForConnection()
  // Arriving DURING the backfill. seq 2 is also in the page, so it must appear
  // once; seq 3 is above maxSeq, the paired positive control, so it must appear.
  conn.emit('task_log', logEvent(2))
  conn.emit('task_log', logEvent(3))
  await tick()
  release()

  await waitFor(() => expect(result.current.status).toBe('live'))
  await waitFor(() =>
    expect(result.current.rows.map((r) => r.text)).toEqual(['line-1', 'line-2', 'line-3']),
  )
})

test('pumps since_seq from next_seq until next_seq is 0', async () => {
  const fake = fakeSseServer()
  const seen: (string | null)[] = []
  server.use(
    http.get('/v1/tasks/t1/logs', ({ request }) => {
      const since = new URL(request.url).searchParams.get('since_seq')
      seen.push(since)
      if (since === null) return HttpResponse.json({ items: [entry(10)], next_seq: 10, total: 3 })
      if (since === '10') return HttpResponse.json({ items: [entry(20)], next_seq: 20, total: 3 })
      return HttpResponse.json({ items: [entry(30)], next_seq: 0, total: 3 })
    }),
  )
  const { result } = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  await waitFor(() => expect(result.current.status).toBe('live'))
  expect(seen).toEqual([null, '10', '20'])
  expect(result.current.rows.map((r) => r.text)).toEqual(['line-10', 'line-20', 'line-30'])
  expect(result.current.historyTruncated).toBe(false)
  expect(result.current.total).toBe(3)
})

test('stops at MAX_BACKFILL_PAGES, flags truncation, and still applies live frames', async () => {
  const fake = fakeSseServer()
  let requests = 0
  server.use(
    http.get('/v1/tasks/t1/logs', () => {
      requests++
      // A server that never drains: next_seq is always non-zero.
      return HttpResponse.json({ items: [entry(requests)], next_seq: requests, total: 94312 })
    }),
  )
  const { result } = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  await waitFor(() => expect(result.current.status).toBe('live'))
  // Exact count, not "several": an off-by-one or a missing cap is a request loop.
  expect(requests).toBe(MAX_BACKFILL_PAGES)
  expect(result.current.historyTruncated).toBe(true)
  expect(result.current.total).toBe(94312)

  // Truncated history must not stop live tailing.
  fake.latest().emit('task_log', logEvent(5000, 'after-cap\n'))
  await waitFor(() => expect(result.current.rows.some((r) => r.text === 'after-cap')).toBe(true))
})

test('a 404 on the stream is a terminal error with no retry', async () => {
  const fake = fakeSseServer()
  fake.status = 404
  fake.errorBody = { error: 'task not found' }
  const { result } = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  await waitFor(() => expect(result.current.status).toBe('error'))
  expect(result.current.errorMessage).toContain('task not found')
  // A deleted task or a bad id is not transient: exactly one attempt, and no
  // history request either.
  await tick()
  expect(fake.connections).toHaveLength(1)
})
