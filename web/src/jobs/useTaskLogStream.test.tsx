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

test('opens at the tail with one request', async () => {
  const fake = fakeSseServer()
  const searches: string[] = []
  server.use(
    http.get('/v1/tasks/t1/logs', ({ request }) => {
      searches.push(new URL(request.url).search)
      // A server with more history than one page: prev_seq is non-zero, and
      // next_seq is 0 in every descending response.
      return HttpResponse.json({ items: [entry(93_913), entry(94_312)], next_seq: 0, prev_seq: 93_913, total: 94_312 })
    }),
  )
  const { result } = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  await waitFor(() => expect(result.current.status).toBe('live'))

  // The exact count and the exact query string: a leftover forward walk shows
  // up as a second request, and a forward FIRST request shows up here as a
  // missing order=desc.
  expect(searches).toEqual(['?order=desc&limit=200'])
  expect(result.current.rows.map((r) => r.text)).toEqual(['line-93913', 'line-94312'])
  expect(result.current.total).toBe(94_312)
  expect(result.current.historyTruncated).toBe(false)
})

test('a short tail page means the log is complete', async () => {
  const fake = fakeSseServer()
  server.use(
    http.get('/v1/tasks/t1/logs', () =>
      HttpResponse.json({ items: [entry(1)], next_seq: 0, prev_seq: 0, total: 1 }),
    ),
  )
  const { result } = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  await waitFor(() => expect(result.current.status).toBe('live'))
  expect(result.current.earlierComplete).toBe(true)
})

// The forward loop is now reached only by a RECOVERY: a fresh open takes the
// tail. Entering through a dropped frame is the shortest honest route to it.
test('a recovery pumps since_seq from next_seq until next_seq is 0', async () => {
  const fake = fakeSseServer()
  const searches: string[] = []
  server.use(
    http.get('/v1/tasks/t1/logs', ({ request }) => {
      const url = new URL(request.url)
      searches.push(url.search)
      const since = url.searchParams.get('since_seq')
      if (since === null) return HttpResponse.json({ items: [entry(10)], next_seq: 0, prev_seq: 0, total: 3 })
      if (since === '10') return HttpResponse.json({ items: [entry(20)], next_seq: 20, prev_seq: 0, total: 3 })
      return HttpResponse.json({ items: [entry(30)], next_seq: 0, prev_seq: 0, total: 3 })
    }),
  )
  const { result } = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  await waitFor(() => expect(result.current.status).toBe('live'))
  expect(searches).toEqual(['?order=desc&limit=200'])

  fake.latest().emit('dropped', { reason: 'slow_consumer' })
  await waitFor(() => expect(result.current.status).toBe('live'))

  // Direction is decided by what we hold: maxSeq is 10, so the recovery pages
  // FORWARD and pumps until next_seq is 0. No order parameter on any of them.
  expect(searches).toEqual([
    '?order=desc&limit=200',
    '?limit=200&since_seq=10',
    '?limit=200&since_seq=20',
  ])
  expect(result.current.rows.filter((r) => r.kind === 'line').map((r) => r.text)).toEqual([
    'line-10',
    'line-20',
    'line-30',
  ])
  expect(result.current.historyTruncated).toBe(false)
  expect(result.current.total).toBe(3)
})

test('a recovery stops at MAX_BACKFILL_PAGES, flags truncation, and still applies live frames', async () => {
  const fake = fakeSseServer()
  let forwardRequests = 0
  server.use(
    http.get('/v1/tasks/t1/logs', ({ request }) => {
      const since = new URL(request.url).searchParams.get('since_seq')
      if (since === null) {
        return HttpResponse.json({ items: [entry(1)], next_seq: 0, prev_seq: 0, total: 94_312 })
      }
      forwardRequests++
      // A server that never drains: next_seq is always non-zero.
      return HttpResponse.json({
        items: [entry(forwardRequests + 1)],
        next_seq: forwardRequests + 1,
        prev_seq: 0,
        total: 94_312,
      })
    }),
  )
  const { result } = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  await waitFor(() => expect(result.current.status).toBe('live'))
  expect(result.current.historyTruncated).toBe(false) // a tail open never truncates

  fake.latest().emit('dropped', { reason: 'slow_consumer' })
  await waitFor(() => expect(result.current.historyTruncated).toBe(true))
  // Exact count, not "several": an off-by-one or a missing cap is a request loop.
  expect(forwardRequests).toBe(MAX_BACKFILL_PAGES)
  expect(result.current.total).toBe(94_312)

  fake.latest().emit('task_log', logEvent(5000, 'after-cap\n'))
  await waitFor(() => expect(result.current.rows.some((r) => r.text === 'after-cap')).toBe(true))
})

// H1 regression (code review): the catch around a failing backfill page set
// status='error' and called controller.abort() but never bumped gen, so the
// still-open stream's own promise later rejected/resolved with myGen still
// equal to gen, re-entering recover('closed') - inserting a bogus drop marker
// and scheduling a retry that silently overwrote 'error' with 'reconnecting'.
// This is the epoch-fence shape: aborting without ending the assignment
// (bumping the generation) leaves a stale connection able to write.
test('a failing backfill page settles to error and the dying stream cannot resurrect it', async () => {
  const fake = fakeSseServer()
  let logReqs = 0
  server.use(
    http.get('/v1/tasks/t1/logs', () => {
      logReqs++
      return HttpResponse.json({ error: 'task not found' }, { status: 404 })
    }),
  )
  const { result } = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  await waitFor(() => expect(result.current.status).toBe('error'))
  // Give the aborted stream's own promise every chance to settle and try to
  // re-enter recovery before asserting the final state stuck.
  await tick()
  await tick()
  expect(result.current.status).toBe('error')
  expect(fake.connections).toHaveLength(1)
  expect(logReqs).toBe(1)
  expect(result.current.rows.some((r) => r.kind === 'marker')).toBe(false)
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

// H2 regression (code review): recover(..., 'dropped') always re-subscribed
// immediately with no cap and no delay, and nothing counted consecutive
// drop-recoveries. Server-side drop is caused by slow consumption, so it is
// self-recurring exactly on the high-volume tasks where it matters most - a
// naive fix using `proven` as the gate does not work, because the recovering
// connection usually DOES deliver a frame before being dropped again.
test('repeated dropped frames are bounded: after 2 immediate recoveries, the backoff ladder and 5-attempt cap apply', async () => {
  vi.useFakeTimers()
  try {
    const fake = fakeSseServer()
    server.use(
      http.get('/v1/tasks/t1/logs', () => HttpResponse.json({ items: [], next_seq: 0, total: 0 })),
    )
    const { result } = renderHook(() =>
      useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
    )

    // Two "free" immediate recoveries, matching a single drop event's own
    // no-delay recovery (README.md:1346-1348) - each closed well under
    // RESET_AFTER_MS so neither ever "proves" its connection.
    for (let i = 0; i < 2; i++) {
      const conn = await fake.waitForConnection(i + 1)
      conn.emit('dropped', { reason: 'slow_consumer' })
      conn.close()
      await advance(1)
    }
    // 1 initial connection + 2 recovery-opened connections.
    expect(fake.connections).toHaveLength(3)
    expect(result.current.status).toBe('live')

    // From the 3rd drop onward a self-recurring drop must fall through to the
    // SAME bounded backoff ladder and 5-attempt cap that governs an abnormal
    // close - never an unbounded per-frame resubscribe. Drive it to
    // 'disconnected' or 25 cycles, whichever comes first - 25 to match the
    // review's own probe.
    const retryDelays = [1000, 2000, 4000, 8000, 15000]
    let backoffCycle = 0
    while (result.current.status !== 'disconnected' && fake.connections.length < 25) {
      const conn = fake.latest()
      conn.emit('dropped', { reason: 'slow_consumer' })
      conn.close()
      await advance(retryDelays[Math.min(backoffCycle, retryDelays.length - 1)])
      backoffCycle++
      if (backoffCycle > 10) break // safety valve; must never be reached
    }

    expect(result.current.status).toBe('disconnected')
    // Non-vacuity: an unbounded per-frame resubscribe reaches 25 connections
    // for 25 drop cycles (confirmed empirically against the unfixed hook).
    // The fix caps it far below that.
    expect(fake.connections.length).toBeLessThan(10)
  } finally {
    vi.useRealTimers()
  }
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

// L3 (code review): when a run is cancelled (unmount, task switch, or a live
// dependency change) before its stream has ever finished opening, the old
// code's openStream() promise settled via neither resolveOpen nor
// rejectOpen, so run()'s `await openStream(myGen)` stayed suspended forever -
// pinning that run's whole closure (logState, pending) in memory for the
// page's lifetime. React's StrictMode dev double-mount hits this on every
// mount (mount, immediate cleanup, remount).
//
// This is a pure memory-retention bug with no behavioural surface inside
// jsdom/vitest, which has no heap-inspection primitive: run()'s own catch
// block already re-checks `cancelled || myGen !== gen` and bails out
// immediately regardless of whether openStream's promise ever settles, so
// there is no state difference to assert either way. The closest available
// regression guard: cancelling mid-connect, then letting the aborted fetch's
// rejection propagate LATE (as a real browser would), must never open a new
// connection and must never throw an unhandled rejection.
test('cancelling a run before its stream opens settles cleanly with no zombie side effects', async () => {
  let releaseFetch: (() => void) | null = null
  const gate = new Promise<void>((r) => {
    releaseFetch = r
  })
  let fetchCalls = 0
  const hangingFetchImpl = (async (_input: RequestInfo | URL, init?: RequestInit) => {
    fetchCalls++
    return new Promise<Response>((resolve, reject) => {
      const onAbort = () => reject(new DOMException('The operation was aborted.', 'AbortError'))
      init?.signal?.addEventListener('abort', onAbort)
      void gate.then(() => {
        init?.signal?.removeEventListener('abort', onAbort)
        resolve(
          new Response(new ReadableStream(), {
            status: 200,
            headers: { 'Content-Type': 'text/event-stream' },
          }),
        )
      })
    })
  }) as unknown as typeof fetch

  const { unmount } = renderHook(() =>
    useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: hangingFetchImpl }),
  )
  await tick()
  expect(fetchCalls).toBe(1)

  // Unmount WHILE still connecting: cleanup sets cancelled=true and aborts.
  unmount()
  await tick()

  let unhandled = false
  const onUnhandled = () => {
    unhandled = true
  }
  process.once('unhandledRejection', onUnhandled)
  // Let the aborted fetch's own promise settle LATE, exactly as a real
  // browser would once the abort actually propagates to the in-flight
  // request.
  releaseFetch!()
  await tick()
  await tick()
  process.removeListener('unhandledRejection', onUnhandled)

  expect(unhandled).toBe(false)
  expect(fetchCalls).toBe(1) // no zombie retry as a side effect of the late settlement
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

// L7 (code review): the carry condition broadened for the manual-reconnect-
// while-live fix (`carry.current?.taskId === taskId`, dropping the `!live`
// requirement) had two uncovered paths. This is the second: reconnect() on a
// task that is, and always was, terminal (live=false from the first render,
// never opened a stream). Debug reading only: reconnect() is UI-unreachable
// here in practice, since LogView only renders the Reconnect control when
// status is 'disconnected', and a `live: false` hook can never reach
// 'disconnected' (it never attempts to open a stream at all) - but the hook
// itself has no such guard, so a defensive test covers the call directly.
test('a manual reconnect on an always-terminal task re-backfills from maxSeq and never opens a stream', async () => {
  const fake = fakeSseServer()
  const sinceSeqsRequested: (string | null)[] = []
  server.use(
    http.get('/v1/tasks/t1/logs', ({ request }) => {
      const since = new URL(request.url).searchParams.get('since_seq')
      sinceSeqsRequested.push(since)
      if (since === null) return HttpResponse.json({ items: [entry(1), entry(2)], next_seq: 0, total: 2 })
      return HttpResponse.json({ items: [], next_seq: 0, total: 2 })
    }),
  )
  const { result } = renderHook(() =>
    useTaskLogStream('t1', { live: false, enabled: true, fetchImpl: fake.fetchImpl }),
  )
  await waitFor(() => expect(sinceSeqsRequested).toHaveLength(1))
  expect(fake.connections).toHaveLength(0) // terminal task: never opens a stream
  expect(result.current.rows.map((r) => r.text)).toEqual(['line-1', 'line-2'])

  await act(async () => {
    result.current.reconnect()
  })
  await waitFor(() => expect(sinceSeqsRequested).toHaveLength(2))

  // Still no stream, ever - a manual reconnect on a terminal task is just a
  // re-backfill, never a live subscription.
  expect(fake.connections).toHaveLength(0)
  // Pages from maxSeq (2), never from scratch (null).
  expect(sinceSeqsRequested[1]).toBe('2')
  expect(result.current.rows.map((r) => r.text)).toEqual(['line-1', 'line-2'])
})

// L7 (code review): the OTHER uncovered path through the broadened carry
// condition, and the actual live-browser scenario that exposed the original
// H1-adjacent bug - the existing regression test above triggers reconnect()
// while status is still 'live' (immediately after a no-backoff drop
// recovery); this one drives the hook all the way to 'disconnected' via the
// retry ladder FIRST, matching what a real user actually clicks Reconnect
// against.
test('a manual reconnect from disconnected (not just from live) preserves the marker and pages from maxSeq', async () => {
  vi.useFakeTimers()
  try {
    const fake = fakeSseServer()
    const sinceSeqsRequested: (string | null)[] = []
    server.use(
      http.get('/v1/tasks/t1/logs', ({ request }) => {
        const since = new URL(request.url).searchParams.get('since_seq')
        sinceSeqsRequested.push(since)
        if (since === null) return HttpResponse.json({ items: [entry(1)], next_seq: 0, total: 1 })
        if (since === '1') return HttpResponse.json({ items: [entry(2)], next_seq: 0, total: 2 })
        return HttpResponse.json({ items: [entry(3)], next_seq: 0, total: 3 })
      }),
    )
    const { result } = renderHook(() =>
      useTaskLogStream('t1', { live: true, enabled: true, fetchImpl: fake.fetchImpl }),
    )
    await advance(0)
    expect(result.current.status).toBe('live')

    // One drop: immediate, no-backoff recovery. maxSeq becomes 2, marker present.
    fake.latest().emit('dropped', { reason: 'slow_consumer' })
    await advance(0)
    expect(result.current.status).toBe('live')
    expect(result.current.dropped).toBe(true)

    // Now simulate a genuine outage: every further connection attempt fails
    // outright, so the SECOND drop's own immediate-recovery attempt fails too,
    // and the retry ladder engages and exhausts all 5 attempts down to
    // 'disconnected'.
    fake.status = 500
    fake.errorBody = { error: 'boom' }
    fake.latest().emit('dropped', { reason: 'slow_consumer' })

    const delays = [1000, 2000, 4000, 8000, 15000]
    for (const d of delays) await advance(d)
    expect(result.current.status).toBe('disconnected')
    expect(result.current.dropped).toBe(true)
    expect(result.current.rows.some((r) => r.kind === 'marker')).toBe(true)

    // The server comes back; the user clicks Reconnect from 'disconnected'.
    fake.status = 200
    await act(async () => {
      result.current.reconnect()
    })
    await advance(0)
    expect(result.current.status).toBe('live')

    expect(result.current.dropped).toBe(true)
    expect(result.current.rows.some((r) => r.kind === 'marker')).toBe(true)
    // Pages from maxSeq (2), never from scratch.
    expect(sinceSeqsRequested[sinceSeqsRequested.length - 1]).toBe('2')
  } finally {
    vi.useRealTimers()
  }
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
