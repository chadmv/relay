// Pure log-state logic for the live task-log view. No React, no network, no
// timers - which is why the interesting behaviour of this feature (dedupe, line
// reassembly, the memory cap) is testable with plain function calls.
//
// Log state deliberately does NOT live in the TanStack cache: a live append-only
// stream has no fetch that resolves, no meaningful staleTime and no meaningful
// invalidate (spec Decision 3).

/** Retained line cap, drop-oldest. Postgres already holds the whole log. */
export const MAX_LINES = 2000

/** Pixels off the bottom at which follow-tail switches off. */
export const FOLLOW_EPSILON = 24

/** The in-stream marker inserted when lines may have been missed. */
export const DROP_MARKER_TEXT = 'lines may be missing here'

/**
 * The minimal shape appendEntries needs. Structurally satisfied by BOTH the
 * polling endpoint's LogEntry (web/src/jobs/api.ts:103-108) and the SSE task_log
 * payload (TaskLogEvent). That field-for-field symmetry is a backend guarantee
 * (README.md:1330-1332) and is why one client type covers both surfaces.
 */
export interface LogChunk {
  seq: number
  stream: string
  content: string
  created_at: string
}

export type LogStream = 'stdout' | 'stderr'

export interface LogRow {
  /** Stable React key. Positive for retained rows; negative for provisional partials. */
  key: number
  kind: 'line' | 'marker' | 'partial'
  stream: LogStream
  text: string
  /** created_at of the entry that terminated this line. '' on marker rows. */
  time: string
}

interface PendingPartial {
  text: string
  time: string
}

export interface LogState {
  lines: LogRow[]
  /** Highest seq accepted. The dedupe key of README.md:1341-1342. */
  maxSeq: number
  /** One in-progress trailing fragment per stream, because an entry is not a line. */
  partials: Record<LogStream, PendingPartial | null>
  nextKey: number
  /** The MAX_LINES cap has evicted at least one line. */
  evicted: boolean
  /** A `dropped` frame or an unexpected close happened; the view is no longer provably complete. */
  dropped: boolean
}

export function createLogState(): LogState {
  return {
    lines: [],
    maxSeq: 0,
    partials: { stdout: null, stderr: null },
    nextKey: 1,
    evicted: false,
    dropped: false,
  }
}

function normalizeStream(s: string): LogStream {
  return s === 'stderr' ? 'stderr' : 'stdout'
}

/**
 * Appends entries, discarding any whose seq is at or below maxSeq. Returns the
 * SAME object when nothing was accepted, so the caller can skip a render.
 *
 * There is deliberately NO gap detection: seq comes from a table-wide BIGSERIAL
 * shared by every task, so a gap is normal and acting on one would re-backfill on
 * nearly every frame (README.md:1357-1360). The only drop signals are the
 * `dropped` frame and an unexpected stream close.
 */
export function appendEntries(state: LogState, entries: LogChunk[]): LogState {
  let lines = state.lines
  let maxSeq = state.maxSeq
  let nextKey = state.nextKey
  let changed = false

  for (const e of entries) {
    if (e.seq <= maxSeq) continue
    maxSeq = e.seq
    if (!changed) {
      lines = lines.slice()
      changed = true
    }
    lines.push({
      key: nextKey++,
      kind: 'line',
      stream: normalizeStream(e.stream),
      // Provisional: strip one trailing newline so a single-line entry renders
      // without a blank gap. Task 3 replaces this with real multi-line
      // reassembly (an entry is not a line - chunkWriter.Write copies arbitrary
      // byte ranges, internal/agent/runner.go:285-309).
      text: e.content.endsWith('\n') ? e.content.slice(0, -1) : e.content,
      time: e.created_at,
    })
  }

  if (!changed) return state
  return { ...state, lines, maxSeq, nextKey }
}
