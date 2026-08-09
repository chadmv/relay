import { act, renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { expect, test, vi } from 'vitest'
import { server } from '../test/setup-helpers'
import { fakeSseServer, tick } from '../test/sseStream'
import { FLUSH_MS, MAX_BACKFILL_PAGES, useTaskLogStream } from './useTaskLogStream'

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

// Paired positive control in one test: the same harness, live true vs false.
test('a terminal task opens no stream at all, while a live one opens exactly one', async () => {
  const fake = fakeSseServer()
  server.use(
    http.get('/v1/tasks/t1/logs', () =>
      HttpResponse.json({ items: [entry(1), entry(2)], next_seq: 0, total: 2 }),
    ),
  )
  const terminal = renderHook(() =>
    useTaskLogStream('t1', { live: false, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  await waitFor(() => expect(terminal.result.current.status).toBe('history'))
  expect(terminal.result.current.rows.map((r) => r.text)).toEqual(['line-1', 'line-2'])
  await tick()
  expect(fake.connections).toHaveLength(0)
  terminal.unmount()

  const running = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  await waitFor(() => expect(running.result.current.status).toBe('live'))
  expect(fake.connections).toHaveLength(1)
  running.unmount()
})

test('a task that becomes terminal mid-tail closes the stream and reconciles once', async () => {
  const fake = fakeSseServer()
  const sinceParams: (string | null)[] = []
  server.use(
    http.get('/v1/tasks/t1/logs', ({ request }) => {
      const since = new URL(request.url).searchParams.get('since_seq')
      sinceParams.push(since)
      if (since === null) return HttpResponse.json({ items: [entry(10)], next_seq: 0, total: 1 })
      return HttpResponse.json({ items: [entry(30, 'tail\n')], next_seq: 0, total: 3 })
    }),
  )
  const { result, rerender } = renderHook(
    ({ live }: { live: boolean }) =>
      useTaskLogStream('t1', { live, enabled: true, fetchImpl: fake.fetchImpl }),
    { initialProps: { live: true } },
  )
  await waitFor(() => expect(result.current.status).toBe('live'))
  const conn = fake.latest()
  // A partial with no trailing newline, plus a live line, before the task ends.
  conn.emit('task_log', logEvent(20, 'mid\nno-newline-yet'))
  await waitFor(() => expect(result.current.rows.some((r) => r.text === 'mid')).toBe(true))

  rerender({ live: false })
  await waitFor(() => expect(result.current.status).toBe('ended'))
  expect(conn.aborted).toBe(true)
  expect(fake.connections).toHaveLength(1)
  // Exactly ONE reconciliation page, and it pages from the last seq seen rather
  // than re-fetching the whole history.
  expect(sinceParams).toEqual([null, '20'])
  // The dangling partial ('no-newline-yet') is carried into the reconciliation
  // and completed by the next entry's content ('tail\n'), merging into ONE line
  // - consistent with Task 3's reassembly model (an entry is an arbitrary byte
  // range; a line split across two entries renders as one line). It is NOT
  // treated as an orphaned fragment unrelated to what the reconciliation page
  // returns: the reconciliation page is a continuation of the same byte stream,
  // not a fresh one.
  const texts = result.current.rows.map((r) => r.text)
  expect(texts).toEqual(['line-10', 'mid', 'no-newline-yettail'])
  expect(result.current.rows.every((r) => r.kind !== 'partial')).toBe(true)
})

test('switching tasks opens exactly one stream each and leaves none open', async () => {
  const fake = fakeSseServer()
  server.use(
    http.get('/v1/tasks/:tid/logs', () => HttpResponse.json({ items: [], next_seq: 0, total: 0 })),
  )
  const { result, rerender, unmount } = renderHook(
    ({ id, enabled }: { id: string; enabled: boolean }) =>
      useTaskLogStream(id, { live: true, enabled, fetchImpl: fake.fetchImpl }),
    { initialProps: { id: 't1', enabled: true } },
  )
  await waitFor(() => expect(result.current.status).toBe('live'))
  expect(fake.connections).toHaveLength(1)

  rerender({ id: 't2', enabled: true })
  await waitFor(() => expect(fake.connections).toHaveLength(2))
  rerender({ id: 't3', enabled: true })
  await waitFor(() => expect(fake.connections).toHaveLength(3))

  // Exact counts, not "at least one": three opened, the first two aborted, and
  // the URLs prove each stream really was for the right task (the positive
  // control that makes the abort assertions meaningful).
  expect(fake.abortedCount()).toBe(2)
  expect(fake.connections[2].aborted).toBe(false)
  expect(fake.connections.map((c) => c.url)).toEqual([
    '/v1/events?task_id=t1',
    '/v1/events?task_id=t2',
    '/v1/events?task_id=t3',
  ])

  // Leaving the Log tab: enabled goes false, the connection closes, none opens.
  rerender({ id: 't3', enabled: false })
  await waitFor(() => expect(fake.abortedCount()).toBe(3))
  expect(fake.connections).toHaveLength(3)
  await waitFor(() => expect(result.current.status).toBe('idle'))
  expect(result.current.rows).toEqual([])

  // Returning re-subscribes exactly once; unmount aborts the last one.
  rerender({ id: 't3', enabled: true })
  await waitFor(() => expect(fake.connections).toHaveLength(4))
  unmount()
  await waitFor(() => expect(fake.abortedCount()).toBe(4))
})

// Regression: found via a live browser check against a real backend (killing
// relay-server mid-tail, letting the hook exhaust its 5 retries to
// 'disconnected', then clicking the manual Reconnect control). The manual path
// must behave like the automatic 'closed' recovery path - continue from
// maxSeq and keep the permanent drop marker - not reset to a fresh empty
// state. A fresh reset both re-fetches the entire history from seq 0 AND
// silently drops the marker, which is exactly the "misrepresents an
// incomplete log as complete" failure the marker exists to prevent.
test('a manual reconnect after a drop preserves the marker and pages from maxSeq, not from scratch', async () => {
  const fake = fakeSseServer()
  const sinceSeqsRequested: (string | null)[] = []
  server.use(
    http.get('/v1/tasks/t1/logs', ({ request }) => {
      const since = new URL(request.url).searchParams.get('since_seq')
      sinceSeqsRequested.push(since)
      if (since === null) return HttpResponse.json({ items: [entry(1)], next_seq: 0, total: 1 })
      return HttpResponse.json({ items: [entry(2)], next_seq: 0, total: 2 })
    }),
  )
  const { result } = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  await waitFor(() => expect(result.current.status).toBe('live'))
  expect(result.current.rows.map((r) => r.text)).toEqual(['line-1'])

  // Cause a drop: one immediate re-backfill, marker inserted, maxSeq now 2.
  fake.latest().emit('dropped', { reason: 'slow_consumer' })
  await waitFor(() => expect(result.current.status).toBe('live'))
  expect(result.current.dropped).toBe(true)
  expect(result.current.rows.some((r) => r.kind === 'marker')).toBe(true)
  expect(result.current.rows.filter((r) => r.kind === 'line').map((r) => r.text)).toEqual([
    'line-1',
    'line-2',
  ])

  // Manual reconnect while still live (not the terminal-task carry path).
  await act(async () => {
    result.current.reconnect()
  })
  await waitFor(() => expect(result.current.status).toBe('live'))

  // The marker must still be there, and the reconnect's own backfill must page
  // from maxSeq (2), never from scratch (null/0) - a scratch re-page would
  // duplicate line-1 and line-2, or (per this mock) would re-request since=null.
  expect(result.current.dropped).toBe(true)
  expect(result.current.rows.some((r) => r.kind === 'marker')).toBe(true)
  expect(result.current.rows.filter((r) => r.kind === 'line').map((r) => r.text)).toEqual([
    'line-1',
    'line-2',
  ])
  expect(sinceSeqsRequested[sinceSeqsRequested.length - 1]).toBe('2')
})

test('coalesces a burst of 50 frames into far fewer than 50 renders', async () => {
  vi.useFakeTimers()
  try {
    const fake = fakeSseServer()
    server.use(
      http.get('/v1/tasks/t1/logs', () => HttpResponse.json({ items: [], next_seq: 0, total: 0 })),
    )
    let renders = 0
    const { result } = renderHook(() => {
      renders++
      return useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl })
    })
    await advance(0)
    expect(result.current.status).toBe('live')
    const before = renders

    const conn = fake.latest()
    // Non-vacuity note: emitting all 50 frames in one synchronous loop with no
    // yield between them would let React's automatic batching absorb every
    // resulting setState into one render regardless of whether FLUSH_MS
    // coalescing exists at all - the mutation that deletes the debounce would
    // not turn this test red (confirmed empirically). Real timers are also
    // unusable here: a real per-iteration `tick()` has nondeterministic
    // wall-clock overhead that can itself cross the 100 ms FLUSH_MS window
    // multiple times, making the test flaky in either direction (also
    // confirmed empirically). Fake timers with a 0 ms advance per frame force
    // each frame through its own microtask turn - so every frame is genuinely
    // ingested separately - without ever letting virtual time reach FLUSH_MS
    // until the loop finishes.
    for (let i = 1; i <= 50; i++) {
      conn.emit('task_log', logEvent(i))
      await advance(0)
    }
    // One more flush window lets the debounced update land.
    await advance(FLUSH_MS)
    // Positive control: all 50 lines really arrived, so a broken transport
    // cannot make the render-count assertion pass.
    expect(result.current.rows).toHaveLength(50)
    expect(renders - before).toBeLessThanOrEqual(5)
  } finally {
    vi.useRealTimers()
  }
})
