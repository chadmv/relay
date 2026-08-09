import { act, renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { expect, test, vi } from 'vitest'
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

// Pure fake timers, no shouldAdvanceTime: real time leaking in would make the
// "did not fire early" assertions flaky by a few milliseconds. Every advance is
// wrapped in act() because it flushes React state updates.
async function advance(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms)
  })
}

test('an event: dropped frame produces exactly ONE re-backfill plus a permanent marker', async () => {
  const fake = fakeSseServer()
  let requests = 0
  server.use(
    http.get('/v1/tasks/t1/logs', () => {
      requests++
      return HttpResponse.json({ items: [entry(requests)], next_seq: 0, total: 1 })
    }),
  )
  const { result } = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  await waitFor(() => expect(result.current.status).toBe('live'))
  expect(requests).toBe(1)

  const first = fake.latest()
  first.emit('dropped', { reason: 'slow_consumer' })
  // The server closes the stream immediately after the dropped frame
  // (internal/api/events.go:84-86). That close must NOT trigger a SECOND
  // recovery - hence the exact counts below rather than "at least".
  first.close()

  await waitFor(() => expect(result.current.status).toBe('live'))
  expect(requests).toBe(2)
  expect(fake.connections).toHaveLength(2)
  expect(first.aborted).toBe(true)
  expect(result.current.dropped).toBe(true)
  expect(result.current.rows.some((r) => r.kind === 'marker')).toBe(true)
  // No backoff delay for a dropped frame: the server told us, so one immediate
  // recovery is correct and the attempt counter is untouched.
  expect(result.current.attempt).toBe(0)

  // The marker is permanent for the session even though recovery succeeded.
  fake.latest().emit('task_log', logEvent(500, 'recovered\n'))
  await waitFor(() => expect(result.current.rows.some((r) => r.text === 'recovered')).toBe(true))
  expect(result.current.rows.some((r) => r.kind === 'marker')).toBe(true)
})

test('reconnects at 1/2/4/8/15 s, stops after 5 attempts, and the manual control resets', async () => {
  vi.useFakeTimers()
  try {
    const fake = fakeSseServer()
    // A 500 fails before the stream ever opens, so this test needs no /logs
    // handler and never leaves an unproven connection ambiguous.
    fake.status = 500
    fake.errorBody = { error: 'boom' }
    const { result } = renderHook(() =>
      useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
    )
    await advance(0)
    expect(fake.connections).toHaveLength(1)
    expect(result.current.status).toBe('reconnecting')
    expect(result.current.attempt).toBe(1)

    const delays = [1000, 2000, 4000, 8000, 15000]
    for (let i = 0; i < delays.length; i++) {
      await advance(delays[i] - 1)
      expect(fake.connections, `retry ${i + 1} fired early`).toHaveLength(i + 1)
      await advance(1)
      expect(fake.connections, `retry ${i + 1} did not fire`).toHaveLength(i + 2)
    }

    // Non-vacuity: the count must STOP growing. A test that only asserted "it
    // retried" passes for an unbounded loop. 50 open tabs against a restarted
    // server must not become a reconnect storm.
    expect(result.current.status).toBe('disconnected')
    await advance(300_000)
    expect(fake.connections).toHaveLength(6) // initial attempt plus exactly 5 retries

    await act(async () => {
      result.current.reconnect()
    })
    await advance(0)
    expect(fake.connections).toHaveLength(7)
    expect(result.current.attempt).toBe(1)
  } finally {
    vi.useRealTimers()
  }
})

test('the backoff counter resets only for a connection that PROVED itself', async () => {
  vi.useFakeTimers()
  try {
    const fake = fakeSseServer()
    server.use(http.get('/v1/tasks/t1/logs', () => HttpResponse.json({ items: [], next_seq: 0, total: 0 })))

    // Direction A: opens and closes immediately, never delivering a frame. This
    // is the 2026-06-20-reconnect-backoff-never-resets bug class - resetting on
    // open alone turns it into an unbounded tight loop.
    //
    // Each connection is closed with ZERO idle time: advance by exactly the
    // retry delay that opens the NEXT connection, then close it before any
    // further time passes. A flat 20s advance per cycle (as a naive version of
    // this test might use) would leave each newly-opened retry connection idle
    // past RESET_AFTER_MS (10s) before the test gets to close it, wrongly
    // marking it "proven" and defeating the very property under test.
    const retryDelays = [1000, 2000, 4000, 8000, 15000]
    const { result, unmount } = renderHook(() =>
      useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
    )
    for (let i = 0; i < 6; i++) {
      const conn = await fake.waitForConnection(i + 1)
      conn.close()
      // The 6th (final) close has no further retry delay to advance past - it
      // transitions straight to 'disconnected' - but that transition still runs
      // through a microtask (the closed stream's read() resolving), so it needs
      // a flush even with 0ms of fake time to elapse.
      await advance(i < retryDelays.length ? retryDelays[i] : 0)
    }
    expect(result.current.status).toBe('disconnected')
    expect(fake.connections).toHaveLength(6)
    await advance(300_000)
    expect(fake.connections).toHaveLength(6)
    unmount()

    // Direction B: same flapping, but each connection delivers a frame first, so
    // each one has proven itself and the counter resets every cycle. Ten cycles
    // must never reach 'disconnected', and every delay must be the first one.
    const fake2 = fakeSseServer()
    const r2 = renderHook(() =>
      useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake2.fetchImpl }),
    )
    for (let i = 0; i < 10; i++) {
      const conn = await fake2.waitForConnection(i + 1)
      conn.emit('task_log', logEvent(i + 1))
      await advance(0)
      conn.close()
      await advance(999)
      expect(fake2.connections, `cycle ${i} retried early`).toHaveLength(i + 1)
      await advance(1)
      expect(fake2.connections, `cycle ${i} did not retry at 1s`).toHaveLength(i + 2)
      expect(r2.result.current.status).not.toBe('disconnected')
    }
    r2.unmount()
  } finally {
    vi.useRealTimers()
  }
})
