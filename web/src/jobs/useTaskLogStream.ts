import { useCallback, useEffect, useMemo, useState } from 'react'
import { ApiError } from '../lib/api'
import {
  BACKFILL_PAGE_SIZE,
  getTaskLogs,
  streamTaskLog,
  type TaskLogEvent,
  type TaskLogPage,
} from './api'
import {
  appendEntries,
  createLogState,
  markDropped,
  visibleRows,
  type LogChunk,
  type LogRow,
  type LogState,
} from './logBuffer'

/** History costs at most 10 requests of 200 lines. */
export const MAX_BACKFILL_PAGES = 10

/** Frames are coalesced into one state update per window. */
export const FLUSH_MS = 100

/** Delays for consecutive failed reconnects, last value repeated. */
export const RETRY_DELAYS_MS = [1000, 2000, 4000, 8000, 15000]

/** Consecutive failed attempts before a human click is required. */
export const MAX_RECONNECT_ATTEMPTS = 5

/** A connection that stays open this long has proven itself. */
export const RESET_AFTER_MS = 10_000

export type LogStreamStatus =
  | 'idle'
  | 'loading'
  | 'live'
  | 'recovering'
  | 'reconnecting'
  | 'disconnected'
  | 'ended'
  | 'history'
  | 'error'

export interface TaskLogStreamResult {
  rows: LogRow[]
  status: LogStreamStatus
  /** Current reconnect attempt, 1..5, shown as "reconnecting (n/5)". */
  attempt: number
  dropped: boolean
  evicted: boolean
  historyTruncated: boolean
  /** `total` from the last page, for the honest "showing N of T" notice. */
  total: number
  errorMessage: string
  reconnect: () => void
}

export interface UseTaskLogStreamOptions {
  /** False when the task is terminal: a terminal task opens no connection at all. */
  live: boolean
  /** False when the Log tab is not showing or no task is selected. */
  enabled: boolean
  /** Test seam forwarded to streamTaskLog. Must be referentially stable. */
  fetchImpl?: typeof fetch
}

/**
 * Tails one task's log: subscribe, then backfill, then replay the buffered
 * frames, then keep appending live ones.
 *
 * State lives HERE and not in the TanStack cache. A live append-only stream has
 * no fetch that resolves, no meaningful staleTime and no meaningful invalidate;
 * the subscribe-before-backfill ordering is imperative sequencing that useQuery
 * cannot express; and paging is a loop with a cap and an early exit rather than
 * one request (spec Decision 3). Log content is therefore never written to the
 * query cache, localStorage or sessionStorage - it is component-lifetime memory,
 * discarded on unmount.
 */
export function useTaskLogStream(
  taskId: string,
  { live, enabled, fetchImpl }: UseTaskLogStreamOptions,
): TaskLogStreamResult {
  const [view, setView] = useState<LogState>(createLogState)
  const [status, setStatus] = useState<LogStreamStatus>('idle')
  const [attempt, setAttempt] = useState(0)
  const [total, setTotal] = useState(0)
  const [historyTruncated, setHistoryTruncated] = useState(false)
  const [errorMessage, setErrorMessage] = useState('')
  const [manualRetry, setManualRetry] = useState(0)

  const reconnect = useCallback(() => {
    setAttempt(0)
    setManualRetry((n) => n + 1)
  }, [])

  useEffect(() => {
    if (!enabled || taskId === '') {
      setView(createLogState())
      setStatus('idle')
      return
    }

    // Every mutable per-connection value is local to this effect run, so a stale
    // run can never write into the current one. `cancelled` plus the `gen`
    // generation counter are the identity check: a callback whose generation is
    // no longer current returns immediately, which is the SPA analogue of the
    // codebase's identity-checked-teardown rule.
    let cancelled = false
    let gen = 0
    let controller = new AbortController()
    let flushTimer: ReturnType<typeof setTimeout> | null = null
    let logState = createLogState()
    // maxSeq and the pre-backfill buffer live here, NOT in React state: writing
    // to them must never trigger a render and never reorder the join.
    let buffering = true
    let pending: TaskLogEvent[] = []
    let retryTimer: ReturnType<typeof setTimeout> | null = null
    let openTimer: ReturnType<typeof setTimeout> | null = null
    let attempts = 0
    let proven = false

    setView(logState)
    setStatus('loading')
    setHistoryTruncated(false)
    setErrorMessage('')

    function publish() {
      flushTimer = null
      if (cancelled) return
      setView(logState)
    }

    // Coalesce to one setState per FLUSH_MS. This is not only a render
    // optimization: a browser that stops draining the socket fills the server's
    // 64-slot buffer and gets drop-closed (README.md:1346-1348), so less React
    // work per frame directly reduces server-side drops.
    function ingest(entries: LogChunk[]) {
      const next = appendEntries(logState, entries)
      if (next === logState) return // everything was a duplicate
      logState = next
      if (flushTimer === null) flushTimer = setTimeout(publish, FLUSH_MS)
    }

    function flushNow() {
      if (flushTimer !== null) {
        clearTimeout(flushTimer)
        flushTimer = null
      }
      publish()
    }

    // A connection earns a backoff-counter reset only by PROVING itself: staying
    // open past RESET_AFTER_MS or delivering at least one frame. Resetting on
    // open alone is exactly the bug relay already shipped once on the agent side
    // (docs/retros/2026-06-20-reconnect-backoff-never-resets.md), where a
    // connection that opens and immediately fails becomes an unbounded tight
    // loop.
    function markProven() {
      proven = true
      if (attempts !== 0) {
        attempts = 0
        setAttempt(0)
      }
    }

    function recover(myGen: number, reason: 'dropped' | 'closed') {
      if (cancelled || myGen !== gen) return
      // Bump the generation FIRST so the dying connection's remaining callbacks
      // cannot trigger a second recovery: the server writes `dropped` and then
      // closes, so both fire for one event.
      gen++
      controller.abort()
      if (openTimer !== null) {
        clearTimeout(openTimer)
        openTimer = null
      }
      // Lines may have been missed either way, so the permanent marker goes in
      // for both reasons.
      logState = markDropped(logState)
      flushNow()

      if (reason === 'dropped') {
        // The server told us it dropped us (README.md:1346-1348). One immediate
        // recovery is correct: no backoff, attempt counter untouched.
        setStatus('recovering')
        void run(logState.maxSeq)
        return
      }
      // A clean close is abnormal (README.md:1310-1313), so it is treated as a
      // failure for backoff purposes, not as an end of data.
      scheduleRetry(logState.maxSeq)
    }

    function scheduleRetry(sinceSeq: number) {
      if (proven) attempts = 0
      if (attempts >= MAX_RECONNECT_ATTEMPTS) {
        setStatus('disconnected')
        return
      }
      const delay = RETRY_DELAYS_MS[Math.min(attempts, RETRY_DELAYS_MS.length - 1)]
      attempts++
      setAttempt(attempts)
      setStatus('reconnecting')
      retryTimer = setTimeout(() => {
        retryTimer = null
        if (!cancelled) void run(sinceSeq)
      }, delay)
    }

    // Resolves once the response is 200, which means handleEvents has already
    // Subscribe()d and flushed (internal/api/events.go:59-70) - so the
    // subscription is provably live. Never a sleep: a timer barrier here is
    // exactly the broken test pattern the enabler's retro caught.
    function openStream(myGen: number): Promise<void> {
      return new Promise<void>((resolveOpen, rejectOpen) => {
        let opened = false
        streamTaskLog(taskId, {
          signal: controller.signal,
          fetchImpl,
          onOpen: () => {
            opened = true
            openTimer = setTimeout(() => {
              openTimer = null
              if (myGen === gen) markProven()
            }, RESET_AFTER_MS)
            resolveOpen()
          },
          onLine: (e) => {
            if (myGen !== gen) return
            markProven() // a delivered frame proves the connection
            if (buffering) pending.push(e)
            else ingest([e])
          },
          onDropped: () => recover(myGen, 'dropped'),
        })
          .then(() => {
            // The server never ends a stream on its own (README.md:1310-1313),
            // so a resolve is abnormal.
            if (opened) recover(myGen, 'closed')
          })
          .catch((err: unknown) => {
            if (cancelled || myGen !== gen) return
            if (opened) recover(myGen, 'closed')
            else rejectOpen(err)
          })
      })
    }

    async function run(sinceSeq: number) {
      const myGen = ++gen
      buffering = true
      pending = []
      controller = new AbortController()
      proven = false

      if (live) {
        try {
          // ORDER IS LOAD-BEARING (README.md:1334-1344): the subscription must be
          // open before the first history page, or the window between the last
          // page and the first frame is lost. Do NOT move this below the paging
          // loop. Guard: useTaskLogStream.test.tsx 'subscribes to the stream
          // BEFORE it requests the first history page'.
          await openStream(myGen)
        } catch (err) {
          if (cancelled || myGen !== gen) return
          if (err instanceof ApiError) {
            // 401 already fired onUnauthorized inside apiStream and
            // AuthProvider redirects (AuthProvider.tsx:39-49). 400 and 404 are
            // not transient: a bad id or a deleted task. No retry for any of
            // them. Never log the error object.
            if (err.status === 400 || err.status === 401 || err.status === 404) {
              setErrorMessage(err.message)
              setStatus('error')
              return
            }
          }
          scheduleRetry(sinceSeq)
          return
        }
        if (cancelled || myGen !== gen) return
      }

      let since = sinceSeq
      let pages = 0
      for (;;) {
        let page: TaskLogPage
        try {
          page = await getTaskLogs(taskId, since, BACKFILL_PAGE_SIZE)
        } catch (err) {
          if (cancelled || myGen !== gen) return
          setErrorMessage(err instanceof Error ? err.message : 'failed to load logs')
          setStatus('error')
          controller.abort()
          return
        }
        if (cancelled || myGen !== gen) return
        ingest(page.items)
        setTotal(page.total)
        pages++
        if (page.next_seq === 0) break
        if (pages >= MAX_BACKFILL_PAGES) {
          setHistoryTruncated(true)
          break
        }
        since = page.next_seq
      }

      // Step 3 of the README join: apply what arrived while we were paging.
      // appendEntries drops anything with seq <= maxSeq, so this is
      // duplicate-free, and both paths share one dedupe rule.
      buffering = false
      const replay = pending
      pending = []
      ingest(replay)
      flushNow()
      setStatus(live ? 'live' : 'history')
    }

    void run(0)

    return () => {
      cancelled = true
      gen++
      controller.abort()
      if (flushTimer !== null) clearTimeout(flushTimer)
      if (retryTimer !== null) clearTimeout(retryTimer)
      if (openTimer !== null) clearTimeout(openTimer)
    }
  }, [taskId, live, enabled, fetchImpl, manualRetry])

  const rows = useMemo(() => visibleRows(view), [view])

  return {
    rows,
    status,
    attempt,
    dropped: view.dropped,
    evicted: view.evicted,
    historyTruncated,
    total,
    errorMessage,
    reconnect,
  }
}
