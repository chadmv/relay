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

// CSI sequences (ESC [ ... final byte) plus OSC (ESC ] ... BEL or ST). Covers SGR
// colour codes, cursor moves and erase-line, which is what a progress bar emits.
// Rendering the colours is a separate proposed follow-up; leaving the raw bytes in
// would show `[32m` litter that reads as corruption. Built via RegExp(string) so
// the ESC control byte never appears literally in this source file.
const ANSI_RE = new RegExp(
  '\\u001B(?:\\[[0-?]*[ -/]*[@-~]|\\][^\\u0007\\u001B]*(?:\\u0007|\\u001B\\\\))',
  'g',
)

function stripAnsi(s: string): string {
  return s.replace(ANSI_RE, '')
}

// Within one emitted line only the segment after the final carriage return is
// kept, so `\rframe 12/100` progress output renders as one updating line instead
// of a wall of concatenated garbage.
function collapseCR(s: string): string {
  const i = s.lastIndexOf('\r')
  return i === -1 ? s : s.slice(i + 1)
}

function capLines(lines: LogRow[]): { lines: LogRow[]; evicted: boolean } {
  if (lines.length <= MAX_LINES) return { lines, evicted: false }
  return { lines: lines.slice(lines.length - MAX_LINES), evicted: true }
}

/**
 * Appends entries, discarding any whose seq is at or below maxSeq, then
 * reassembles their content into LINES. Returns the SAME object when nothing was
 * accepted, so the caller can skip a render.
 *
 * A log entry is NOT a line: chunkWriter.Write copies whatever os/exec hands it
 * (internal/agent/runner.go:285-309), so an entry can hold many lines and one
 * logical line can straddle two entries. One pending-partial buffer per stream
 * handles both cases; every '\n' emits a completed line in the order its
 * terminating newline arrived, which is what a terminal shows for merged output.
 *
 * Dedupe happens BEFORE reassembly, so replaying a buffered frame can never
 * duplicate a partial line.
 *
 * There is deliberately NO gap detection: seq comes from a table-wide BIGSERIAL
 * shared by every task, so a gap is normal and acting on one would re-backfill on
 * nearly every frame (README.md:1357-1360). The only drop signals are the
 * `dropped` frame and an unexpected stream close.
 */
export function appendEntries(state: LogState, entries: LogChunk[]): LogState {
  let lines = state.lines
  let partials = state.partials
  let maxSeq = state.maxSeq
  let nextKey = state.nextKey
  let changed = false

  for (const e of entries) {
    if (e.seq <= maxSeq) continue
    maxSeq = e.seq
    if (!changed) {
      lines = lines.slice()
      partials = { ...partials }
      changed = true
    }

    const stream = normalizeStream(e.stream)
    // Strip before splitting: an escape sequence never contains a newline.
    let buf = (partials[stream]?.text ?? '') + stripAnsi(e.content)

    let nl = buf.indexOf('\n')
    while (nl !== -1) {
      const raw = buf.slice(0, nl)
      buf = buf.slice(nl + 1)
      lines.push({ key: nextKey++, kind: 'line', stream, text: collapseCR(raw), time: e.created_at })
      nl = buf.indexOf('\n')
    }
    partials[stream] = buf === '' ? null : { text: buf, time: e.created_at }
  }

  if (!changed) return state
  const capped = capLines(lines)
  return {
    ...state,
    lines: capped.lines,
    partials,
    maxSeq,
    nextKey,
    evicted: state.evicted || capped.evicted,
  }
}

/**
 * Rows to render: the retained lines plus one provisional row per dangling
 * partial. A task that prints a prompt with no trailing newline must not look
 * silent. Provisional rows use fixed NEGATIVE keys so they can never collide with
 * the positive keys of retained lines and React keeps them stable across renders.
 */
export function visibleRows(state: LogState): LogRow[] {
  const { stdout, stderr } = state.partials
  if (stdout === null && stderr === null) return state.lines
  const rows = state.lines.slice()
  if (stdout !== null) {
    rows.push({ key: -1, kind: 'partial', stream: 'stdout', text: collapseCR(stdout.text), time: stdout.time })
  }
  if (stderr !== null) {
    rows.push({ key: -2, kind: 'partial', stream: 'stderr', text: collapseCR(stderr.text), time: stderr.time })
  }
  return rows
}

/**
 * Flushes any dangling partials into real lines. Called when the task reaches a
 * terminal status: there will be no further output, so a partial is final.
 * Returns the same object when there is nothing pending, so it is idempotent.
 */
export function finalizePartials(state: LogState): LogState {
  const streams: LogStream[] = ['stdout', 'stderr']
  const pending = streams.filter((s) => state.partials[s] !== null)
  if (pending.length === 0) return state

  let nextKey = state.nextKey
  const lines = state.lines.slice()
  for (const s of pending) {
    const p = state.partials[s]!
    lines.push({ key: nextKey++, kind: 'line', stream: s, text: collapseCR(p.text), time: p.time })
  }
  const capped = capLines(lines)
  return {
    ...state,
    lines: capped.lines,
    partials: { stdout: null, stderr: null },
    nextKey,
    evicted: state.evicted || capped.evicted,
  }
}
