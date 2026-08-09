import { afterEach, expect, test, vi } from 'vitest'
import { http, HttpResponse } from 'msw'
import { server } from '../test/setup-helpers'
import { fakeSseServer, tick } from '../test/sseStream'
import { ApiError, apiStream, onUnauthorized } from './api'
import { clearToken, setToken } from './token'
import type { SseFrame } from './sse'

afterEach(() => clearToken())

function frameOf(seq: number) {
  return { seq, stream: 'stdout', content: `line-${seq}\n`, created_at: '2026-08-09T00:00:00Z' }
}

test('sends the bearer token and the /v1 prefix', async () => {
  const fake = fakeSseServer()
  setToken('tok-123')
  const ac = new AbortController()
  const p = apiStream('/events?task_id=t1', {
    signal: ac.signal,
    onEvent: () => {},
    fetchImpl: fake.fetchImpl,
  })
  const conn = await fake.waitForConnection()
  expect(conn.url).toBe('/v1/events?task_id=t1')
  expect(conn.headers.get('Authorization')).toBe('Bearer tok-123')
  expect(conn.headers.get('Accept')).toBe('text/event-stream')
  ac.abort()
  await expect(p).rejects.toThrow()
})

test('a 401 fires the onUnauthorized listeners and does not retry', async () => {
  const fake = fakeSseServer()
  fake.status = 401
  fake.errorBody = { error: 'unauthorized' }
  const seen = vi.fn()
  const off = onUnauthorized(seen)
  await expect(
    apiStream('/events?task_id=t1', {
      signal: new AbortController().signal,
      onEvent: () => {},
      fetchImpl: fake.fetchImpl,
    }),
  ).rejects.toBeInstanceOf(ApiError)
  // Without this, a revoked token becomes a silently empty log instead of a
  // redirect to sign-in (AuthProvider.tsx:39-49 is the subscriber).
  expect(seen).toHaveBeenCalledTimes(1)
  // apiStream never retries: recovery policy belongs to the hook.
  expect(fake.connections).toHaveLength(1)
  off()
})

test('a 404 throws ApiError(404, "task not found") before any frame is delivered', async () => {
  const fake = fakeSseServer()
  fake.status = 404
  fake.errorBody = { error: 'task not found' }
  const frames: SseFrame[] = []
  await expect(
    apiStream('/events?task_id=nope', {
      signal: new AbortController().signal,
      onEvent: (f) => frames.push(f),
      fetchImpl: fake.fetchImpl,
    }),
  ).rejects.toMatchObject({ status: 404, code: 'task not found' })
  // A 404 must be distinguishable from an empty log.
  expect(frames).toHaveLength(0)
})

// THE assertion the spec forbids weakening. A buffering transport passes a naive
// "both frames arrived at the end" test and fails this one.
test('delivers the first frame BEFORE the stream closes', async () => {
  const fake = fakeSseServer()
  const frames: SseFrame[] = []
  let opened = false
  const p = apiStream('/events?task_id=t1', {
    signal: new AbortController().signal,
    onOpen: () => {
      opened = true
    },
    onEvent: (f) => frames.push(f),
    fetchImpl: fake.fetchImpl,
  })
  const conn = await fake.waitForConnection()
  await tick()
  // onOpen fires on the 200, which is when handleEvents has already Subscribe()d
  // and flushed (internal/api/events.go:59-70).
  expect(opened).toBe(true)

  conn.emit('task_log', frameOf(1))
  await tick()
  expect(frames).toHaveLength(1)
  expect(frames[0].event).toBe('task_log')
  expect(JSON.parse(frames[0].data).seq).toBe(1)

  conn.emit('task_log', frameOf(2))
  conn.close()
  await p
  expect(frames).toHaveLength(2)
})

test('abort stops delivery and the transport sees the abort', async () => {
  const fake = fakeSseServer()
  const frames: SseFrame[] = []
  const ac = new AbortController()
  const p = apiStream('/events?task_id=t1', {
    signal: ac.signal,
    onEvent: (f) => frames.push(f),
    fetchImpl: fake.fetchImpl,
  })
  const conn = await fake.waitForConnection()
  conn.emit('task_log', frameOf(1))
  await tick()
  expect(frames).toHaveLength(1) // positive control on the same path

  ac.abort()
  await expect(p).rejects.toThrow()
  // The real leak property: the signal reached the transport.
  expect(conn.aborted).toBe(true)

  conn.emit('task_log', frameOf(2))
  await tick()
  expect(frames).toHaveLength(1) // nothing after abort
})

// The default fetchImpl path, through MSW, so the seam cannot hide a broken
// default. These two do not depend on incremental delivery.
//
// Signal note: these two tests deliberately do NOT construct a real
// AbortController. Under vitest's jsdom environment, jsdom's AbortController is a
// distinct realm from the one Node's native fetch validates a `signal` against
// (confirmed empirically: `fetch(url, { signal: new AbortController().signal })`
// throws `TypeError: RequestInit: Expected signal to be an instance of
// AbortSignal` even with zero MSW involvement - jsdom's window.AbortController
// overwrites the global after Node's fetch has already bound to its own). This is
// a test-environment artifact only: a real browser has one realm, so
// `apiStream`'s production code is untouched and still types `signal` as
// required. Omitting the field here (not "weakening" - neither test exercises
// abort) sidesteps the realm mismatch without touching the seam-based tests
// above, which never hit real fetch and are unaffected.
test('through the real global fetch (MSW): the bearer header is attached', async () => {
  setToken('tok-msw')
  let auth: string | null = null
  server.use(
    http.get('/v1/events', ({ request }) => {
      auth = request.headers.get('Authorization')
      return HttpResponse.json({ error: 'stop here' }, { status: 400 })
    }),
  )
  await expect(
    apiStream('/events?task_id=t1', { signal: undefined as unknown as AbortSignal, onEvent: () => {} }),
  ).rejects.toThrow()
  expect(auth).toBe('Bearer tok-msw')
})

test('through the real global fetch (MSW): a 404 envelope becomes ApiError', async () => {
  server.use(http.get('/v1/events', () => HttpResponse.json({ error: 'task not found' }, { status: 404 })))
  await expect(
    apiStream('/events?task_id=nope', { signal: undefined as unknown as AbortSignal, onEvent: () => {} }),
  ).rejects.toMatchObject({ status: 404, code: 'task not found' })
})

// EMPIRICAL, verified 2026-08-09: MSW 2.7 + undici under jsdom 29 DOES deliver a
// ReadableStream body incrementally (this test is green and stable across
// repeated runs). The fetchImpl seam and its own 'delivers the first frame
// BEFORE the stream closes' test above remain regardless (spec Testing 12) - do
// not delete them just because MSW happens to work here.
test('through the real global fetch (MSW): the first frame arrives before close', async () => {
  let ctl!: ReadableStreamDefaultController<Uint8Array>
  const enc = new TextEncoder()
  const body = new ReadableStream<Uint8Array>({ start: (c) => { ctl = c } })
  server.use(
    http.get('/v1/events', () =>
      new Response(body, { status: 200, headers: { 'Content-Type': 'text/event-stream' } }),
    ),
  )
  const frames: SseFrame[] = []
  const p = apiStream('/events?task_id=t1', {
    signal: undefined as unknown as AbortSignal,
    onEvent: (f) => frames.push(f),
  })
  await tick()
  ctl.enqueue(enc.encode(`event: task_log\ndata: ${JSON.stringify(frameOf(1))}\n\n`))
  await tick()
  expect(frames).toHaveLength(1) // observed while the stream is still open
  ctl.close()
  await p
  expect(frames).toHaveLength(1)
})
