import { getToken } from './token'
import { createSseParser, type SseFrame } from './sse'

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

type Listener = () => void
const unauthorizedListeners = new Set<Listener>()

/** Register a callback fired whenever a request returns 401. Returns an unsubscribe fn. */
export function onUnauthorized(fn: Listener): () => void {
  unauthorizedListeners.add(fn)
  return () => unauthorizedListeners.delete(fn)
}

interface ApiOptions extends Omit<RequestInit, 'body'> {
  json?: unknown
}

/** Fetch wrapper: prefixes /v1, attaches bearer token, parses the {error} envelope. */
export async function apiFetch<T = unknown>(path: string, opts: ApiOptions = {}): Promise<T> {
  const { json, headers, ...rest } = opts
  const finalHeaders = new Headers(headers)
  const token = getToken()
  if (token) finalHeaders.set('Authorization', `Bearer ${token}`)
  if (json !== undefined) {
    finalHeaders.set('Content-Type', 'application/json')
  }

  const res = await fetch(`/v1${path}`, {
    ...rest,
    headers: finalHeaders,
    body: json !== undefined ? JSON.stringify(json) : undefined,
  })

  if (res.status === 401) {
    unauthorizedListeners.forEach((fn) => fn())
  }

  if (!res.ok) {
    const code = await res
      .json()
      .then((b) => (b as { error?: string }).error ?? 'error')
      .catch(() => 'error')
    throw new ApiError(res.status, code, `${res.status} ${code}`)
  }

  // 204 (revoke) and 202 (evict, best-effort async) return no body.
  if (res.status === 204 || res.status === 202) return undefined as T
  return (await res.json()) as T
}

export interface StreamOptions {
  signal: AbortSignal
  onEvent: (frame: SseFrame) => void
  /** Called once the response is 200, i.e. once the subscription is live. */
  onOpen?: () => void
  /** Test seam; defaults to globalThis.fetch. See the note below - do not delete it. */
  fetchImpl?: typeof fetch
}

// Test-environment-only compatibility shim. Under vitest's jsdom environment,
// `AbortController`/`AbortSignal` on globalThis are jsdom's own classes (a
// distinct realm from Node's native fetch, which - confirmed empirically -
// rejects a jsdom-constructed AbortSignal with this exact TypeError even with
// zero MSW involvement, and even the npm `undici` package's own fetch rejects
// it the same way, because the check is an `instanceof` against a class
// reference internal to Node's bundled undici, unreachable from userland once
// jsdom's environment setup has overwritten globalThis.AbortSignal). This NEVER
// happens in a real browser, where these classes share one realm, and it never
// happens in any test that injects `fetchImpl` (the seam does not validate
// signal identity), so this fallback is dead code outside this one scenario:
// a component test exercising useTaskLogStream's default (real fetch) path
// through MSW, as JobDetailPage.test.tsx and TaskLogPage.test.tsx do.
function isAbortSignalRealmMismatch(err: unknown): boolean {
  return err instanceof TypeError && /AbortSignal/.test(err.message)
}

/**
 * Opens an authenticated Server-Sent Events stream, calling onEvent for every
 * frame. Resolves when the stream ends; rejects on a non-ok response, a transport
 * error, or an abort.
 *
 * It lives HERE, next to apiFetch, on purpose: the bearer token (token.ts:3-5) is
 * attached in exactly one place, and a streaming 401 fires the same
 * onUnauthorized notifier AuthProvider subscribes to (AuthProvider.tsx:39-49).
 * Otherwise a revoked token would turn into a silently empty log instead of a
 * redirect to sign-in. sse.ts holds framing only and knows nothing about auth.
 *
 * EventSource is deliberately not used: it cannot set an Authorization header,
 * and putting the token in a query parameter would leak the SPA's only credential
 * into proxy logs, browser history and Referer. Losing EventSource's automatic
 * reconnect is a feature - the retry here must be bounded, which EventSource
 * cannot do.
 *
 * `fetchImpl` is a test seam mirroring the project's package-var-override
 * convention (internal/cli's saveConfigFn and friends). It exists because MSW is
 * an interception layer, not a socket, so incremental delivery through it is not
 * guaranteed. DO NOT DELETE IT even if MSW happens to stream correctly.
 */
export async function apiStream(path: string, opts: StreamOptions): Promise<void> {
  const { signal, onEvent, onOpen, fetchImpl } = opts
  const doFetch = fetchImpl ?? globalThis.fetch

  const headers = new Headers({ Accept: 'text/event-stream' })
  const token = getToken()
  if (token) headers.set('Authorization', `Bearer ${token}`)

  let res: Response
  let signalForwarded = true
  try {
    res = await doFetch(`/v1${path}`, { headers, signal })
  } catch (err) {
    if (!isAbortSignalRealmMismatch(err)) throw err
    signalForwarded = false
    res = await doFetch(`/v1${path}`, { headers })
  }

  if (res.status === 401) {
    unauthorizedListeners.forEach((fn) => fn())
  }

  // A non-ok response arrives as JSON BEFORE the headers switch to
  // text/event-stream (internal/api/events.go:34-43), so a 404 "task not found"
  // is distinguishable from an empty log. Same envelope handling as apiFetch.
  if (!res.ok) {
    const code = await res
      .json()
      .then((b) => (b as { error?: string }).error ?? 'error')
      .catch(() => 'error')
    throw new ApiError(res.status, code, `${res.status} ${code}`)
  }
  if (!res.body) {
    throw new ApiError(res.status, 'no stream body', `${res.status} no stream body`)
  }

  // A 200 means handleEvents has already Subscribe()d and flushed
  // (internal/api/events.go:59-70), so the subscription is live. That is what the
  // caller waits on before it starts backfilling - never a timer.
  onOpen?.()

  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  const parser = createSseParser()

  // The realm-mismatch fallback skips forwarding `signal` to fetch, so if the
  // caller already aborted BEFORE or DURING that fallback call, the 'abort'
  // event already fired in the past - a listener registered now would never
  // see it, and the read loop below would await reader.read() forever with
  // nothing telling it to stop. Handle the already-aborted case explicitly,
  // before ever entering the loop.
  if (!signalForwarded && signal.aborted) {
    await reader.cancel().catch(() => {})
    return
  }

  // Only needed when the realm-mismatch fallback above skipped forwarding
  // signal to fetch: cancelling the reader directly is a spec-valid way to
  // abort a fetch body read, so abort still works even without fetch's own
  // native signal wiring. Harmless to skip when the signal WAS forwarded -
  // fetch already tears the stream down on abort in that case.
  const onAbort = () => {
    reader.cancel().catch(() => {})
  }
  if (!signalForwarded) signal.addEventListener('abort', onAbort)

  try {
    for (;;) {
      const { done, value } = await reader.read()
      if (done) return
      // Never log a frame: content is raw subprocess output and can carry secrets
      // a job's own script echoed.
      for (const frame of parser.push(decoder.decode(value, { stream: true }))) onEvent(frame)
    }
  } finally {
    if (!signalForwarded) signal.removeEventListener('abort', onAbort)
    // cancel() closes the stream and releases the lock in one step, so an aborted
    // or abandoned stream never leaves a dangling reader.
    await reader.cancel().catch(() => {})
  }
}
