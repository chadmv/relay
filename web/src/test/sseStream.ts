// Transport test harness for the SSE client. It exists because MSW is an
// interception layer, not a socket: whether it delivers a ReadableStream body
// INCREMENTALLY under jsdom is not guaranteed, and a buffering layer would make
// an incremental-delivery assertion silently vacuous. fakeSseServer() is injected
// through apiStream's `fetchImpl` seam (the SPA analogue of internal/cli's
// saveConfigFn override convention), so frame timing is fully under test control.

export interface FakeSseConnection {
  url: string
  headers: Headers
  /** True once the caller's AbortSignal fired. The leak assertions read this. */
  aborted: boolean
  /** True once the consumer cancelled the body. */
  cancelled: boolean
  /** Pushes raw SSE text into the body. */
  send(text: string): void
  /** Pushes one well-formed frame: `event: <type>\ndata: <json>\n\n`. */
  emit(event: string, data: unknown): void
  /** Ends the body cleanly. relay's server never does this, which is why it is abnormal. */
  close(): void
  /** Errors the body, simulating network loss or a server restart. */
  fail(err?: unknown): void
}

export interface FakeSseServer {
  fetchImpl: typeof fetch
  /** Every connection ever opened, in order. Length = streams opened. */
  connections: FakeSseConnection[]
  /** Status for the NEXT connection. 200 streams; anything else is a JSON error body. */
  status: number
  /** Body for a non-200 response, matching writeError's {error} envelope. */
  errorBody: unknown
  latest(): FakeSseConnection
  abortedCount(): number
  /** Resolves once at least n connections exist. Microtask-based, so it works under fake timers. */
  waitForConnection(n?: number): Promise<FakeSseConnection>
}

export function fakeSseServer(): FakeSseServer {
  const connections: FakeSseConnection[] = []
  const enc = new TextEncoder()

  const server: FakeSseServer = {
    connections,
    status: 200,
    errorBody: { error: 'error' },
    fetchImpl: (async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : String(input)
      const headers = new Headers(init?.headers)

      // `connections` records every fetch ATTEMPT, not only successful streams:
      // "no retry" assertions (a 401/404/500 status) need to see exactly one
      // attempt even though no stream ever opens for those.
      let ctl: ReadableStreamDefaultController<Uint8Array> | null = null
      const conn: FakeSseConnection = {
        url,
        headers,
        aborted: false,
        cancelled: false,
        send(text) {
          try {
            ctl?.enqueue(enc.encode(text))
          } catch {
            /* already closed or errored */
          }
        },
        emit(event, data) {
          conn.send(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`)
        },
        close() {
          try {
            ctl?.close()
          } catch {
            /* already closed */
          }
        },
        fail(err = new TypeError('network error')) {
          try {
            ctl?.error(err)
          } catch {
            /* already closed */
          }
        },
      }
      init?.signal?.addEventListener('abort', () => {
        conn.aborted = true
        conn.fail(new DOMException('The operation was aborted.', 'AbortError'))
      })
      connections.push(conn)

      if (server.status !== 200) {
        // A non-ok response arrives as JSON BEFORE the headers switch to
        // text/event-stream, exactly as internal/api/events.go:34-43 writes it.
        return new Response(JSON.stringify(server.errorBody), {
          status: server.status,
          headers: { 'Content-Type': 'application/json' },
        })
      }

      const body = new ReadableStream<Uint8Array>({
        start(c) {
          ctl = c
        },
        cancel() {
          conn.cancelled = true
        },
      })
      return new Response(body, {
        status: 200,
        headers: { 'Content-Type': 'text/event-stream' },
      })
    }) as unknown as typeof fetch,
    latest: () => connections[connections.length - 1],
    abortedCount: () => connections.filter((c) => c.aborted).length,
    async waitForConnection(n = 1) {
      for (let i = 0; i < 500 && connections.length < n; i++) await Promise.resolve()
      if (connections.length < n) {
        throw new Error(`expected ${n} connection(s), only ${connections.length} opened`)
      }
      return connections[n - 1]
    },
  }
  return server
}

/**
 * An MSW resolver body for an SSE endpoint that stays open until the request is
 * aborted. Used by COMPONENT tests that only need "a stream exists" - frame
 * delivery is asserted through the fetchImpl seam above, never through MSW.
 */
export function openSseResponse(): Response {
  const body = new ReadableStream<Uint8Array>({
    start() {
      /* deliberately never closed */
    },
  })
  return new Response(body, { status: 200, headers: { 'Content-Type': 'text/event-stream' } })
}

/** Lets the stream reader loop make progress. One macrotask tick, not a bare microtask drain. */
export function tick(): Promise<void> {
  return new Promise((r) => setTimeout(r, 0))
}
