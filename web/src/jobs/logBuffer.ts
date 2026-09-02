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
 * polling endpoint's LogEntry (web/src/jobs/api.ts) and the SSE task_log
 * payload (TaskLogEvent). That field-for-field symmetry is a backend guarantee
 * (README.md, "Events (Server-Sent Events)") and is why one client type covers
 * both surfaces.
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
  /** Highest seq accepted. The dedupe key of README.md, "Events (Server-Sent Events)". */
  maxSeq: number
  /** Lowest seq accepted; 0 when the window is empty. The backwards cursor. */
  minSeq: number
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
    minSeq: 0,
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

// Two rules, in this order, and the order is the whole correctness argument.
//
// 1. EVERY trailing carriage return is removed. Not one. CRLF output puts a \r
//    in the final position of every line, and "x\r\r" - two of them - is what
//    the Windows C runtime writes for print("done", end="\r") followed by
//    print(), which is the most ordinary progress-bar-then-newline sequence
//    there is. Stripping a single CR leaves that row blank.
// 2. THEN the progress-bar collapse: only the segment after the final REMAINING
//    carriage return is kept, so a run of \r-updated frames renders as one
//    updating line instead of a wall of concatenated garbage.
//
// Why removing a run of trailing CRs cannot lose visible content: this function
// is only ever called on a complete line (everything before a \n) or on the
// whole of a stream's in-flight partial. In both, a trailing carriage return has
// NO SUCCESSOR INSIDE THE UNIT, so nothing can be written after the cursor
// returns and the CR can overwrite nothing. Removing it can only stop the
// approximation from returning the empty suffix that follows it. That is as true
// for a run as for one, which is why the rule is strip-all rather than merely
// tolerant.
//
// Collapsing FIRST and stripping afterwards does nothing at all: the collapse has
// already returned '' for a line ending in a carriage return.
//
// The trailing-CR strip is an INDEX WALK, deliberately not a `/\r+$/` regex.
// That is NOT because the regex is slow on the input it targets - measured in
// V8 (N = 200,000), a genuinely trailing run is where the regex is FASTEST: it
// matches at its first attempt and beats the walk (0.2 ms vs 0.5 ms). The regex
// is instead catastrophic on a run that is NOT trailing - 200,000 CRs followed
// by one more byte with no terminator yet took 23,637 ms against the walk's
// 0.1 ms - because the engine restarts `\r+` at every offset and fails `$` at
// each one. That exact shape - a long CR burst with more bytes still to come -
// is what a progress bar leaves in an in-flight partial (appendEntries grows
// the partial buffer until a newline arrives; nothing here caps line LENGTH,
// only MAX_LINES caps line COUNT), so the walk's O(n) guarantee has to hold
// unconditionally rather than merely on the input that happens to look tidy.
//
// This runs AFTER stripAnsi (appendEntries strips before it splits), and that
// ordering is in the fix's favour: an erase-line escape sequence sitting between
// a carriage return and the newline is removed first, so a CR the raw bytes had
// buried behind an escape is still trailing by the time we see it. Anything
// upstream of stripAnsi would see fewer carriage returns, not more.
//
// The remaining approximation is unchanged and deliberate: a terminal renders
// 'abc' CR 'd' as `dbc`, and this keeps `d`. That is wrong in the MIDDLE of a
// unit and is out of scope; this makes it right at the END.
//
// Single lastIndexOf, not a strip-then-search-again: searching s for '\r' with
// fromIndex = end - 1 covers exactly the same range as searching a s.slice(0,
// end) copy would, without allocating that intermediate copy. The one edge
// case - end === 0, meaning the whole unit was CRs (or empty) - looks like it
// could return a stray non-(-1) index, because a negative fromIndex clamps to
// 0 rather than signalling "nothing to search"; but the subsequent
// s.slice(i + 1, end) then has start >= end, which JavaScript defines as ''
// regardless of what i was, so the answer is correct anyway. Fuzzed against
// the previous two-step form over 300,000 random strings with 0 mismatches.
function collapseCR(s: string): string {
  let end = s.length
  while (end > 0 && s.charCodeAt(end - 1) === 13) end-- // 13 === '\r'
  const i = s.lastIndexOf('\r', end - 1)
  return i === -1 ? s.slice(0, end) : s.slice(i + 1, end)
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
 * A log entry is NOT a line: chunkWriter.Write (internal/agent/runner.go) copies
 * whatever os/exec hands it, so an entry can hold many lines and one
 * logical line can straddle two entries. One pending-partial buffer per stream
 * handles both cases; every '\n' emits a completed line in the order its
 * terminating newline arrived, which is what a terminal shows for merged output.
 *
 * Dedupe happens BEFORE reassembly, so replaying a buffered frame can never
 * duplicate a partial line.
 *
 * There is deliberately NO gap detection: seq comes from a table-wide BIGSERIAL
 * shared by every task, so a gap is normal and acting on one would re-backfill on
 * nearly every frame (README.md, "Events (Server-Sent Events)"). The only drop
 * signals are the `dropped` frame and an unexpected stream close.
 */
export function appendEntries(state: LogState, entries: LogChunk[]): LogState {
  let lines = state.lines
  let partials = state.partials
  let maxSeq = state.maxSeq
  let minSeq = state.minSeq
  let nextKey = state.nextKey
  let changed = false

  for (const e of entries) {
    if (e.seq <= maxSeq) continue
    maxSeq = e.seq
    if (minSeq === 0) minSeq = e.seq
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
    minSeq,
    nextKey,
    evicted: state.evicted || capped.evicted,
  }
}

/**
 * Prepends an older page. Entries are ascending and every seq is below
 * state.minSeq, so the batch is contiguous with the window by construction -
 * which is what makes the seam join exact: the window's first COMPLETED line of
 * a given stream is the text from the window's start to that stream's first
 * newline, so the batch's dangling fragment for that stream is precisely its
 * missing prefix.
 *
 * Refuses once evicted is set. Drop-oldest has then removed the front of the
 * window, so its first row is no longer the continuation of minSeq and there is
 * no seam to join to.
 *
 * The join skips marker rows: markDropped emits its marker with stream 'stdout'
 * whichever stream dropped, so matching on stream alone would concatenate a
 * fragment onto the drop notice. Guard: 'prependEntries does not join into a
 * marker row'.
 */
export function prependEntries(state: LogState, entries: LogChunk[]): LogState {
  if (state.evicted || entries.length === 0) return state

  // Reuse the tested reassembly rather than forking it.
  const batch = appendEntries(createLogState(), entries)

  const lines = state.lines.slice()
  const partials = { ...state.partials }
  const streams: LogStream[] = ['stdout', 'stderr']
  for (const s of streams) {
    const dangling = batch.partials[s]
    if (dangling === null) continue
    const i = lines.findIndex((r) => r.kind === 'line' && r.stream === s)
    if (i === -1) {
      // No completed line for this stream in the window: the fragment is the
      // prefix of whatever partial is still open there, or is itself the only
      // text that stream has.
      const open = partials[s]
      partials[s] = { text: dangling.text + (open?.text ?? ''), time: open?.time ?? dangling.time }
      continue
    }
    lines[i] = { ...lines[i], text: collapseCR(dangling.text + lines[i].text) }
  }

  let nextKey = state.nextKey
  const prepended = batch.lines.map((r) => ({ ...r, key: nextKey++ }))
  const capped = capLines([...prepended, ...lines])
  return {
    ...state,
    lines: capped.lines,
    partials,
    nextKey,
    minSeq: state.minSeq === 0 ? entries[0].seq : Math.min(state.minSeq, entries[0].seq),
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

/**
 * Records that lines may have been missed: appends a permanent in-stream marker
 * row and sets the dropped flag. The marker stays for the session even after
 * recovery succeeds, because the view is no longer provably complete - silence
 * here would misrepresent an incomplete log as complete, which is the exact
 * failure today's STATIC/HISTORY label exists to avoid.
 *
 * A no-op when the last retained row is ALREADY a marker: a real recovery
 * cycle can call this many times in a row with no intervening line (a normal
 * 5-attempt retry exhaustion, or a run of bounded drop-recoveries), and
 * without this guard each call stacks another marker - 6 markers for one
 * ordinary disconnect, 25 for 25 drop cycles. One marker already says
 * everything a second adjacent one would.
 */
export function markDropped(state: LogState): LogState {
  const last = state.lines[state.lines.length - 1]
  if (last?.kind === 'marker') {
    return state.dropped ? state : { ...state, dropped: true }
  }
  const capped = capLines([
    ...state.lines,
    { key: state.nextKey, kind: 'marker', stream: 'stdout', text: DROP_MARKER_TEXT, time: '' },
  ])
  return {
    ...state,
    lines: capped.lines,
    nextKey: state.nextKey + 1,
    evicted: state.evicted || capped.evicted,
    dropped: true,
  }
}

/**
 * Whether follow-tail should stay on given a scroll container's geometry. The
 * whole threshold decision is extracted as a pure function because the pixel
 * effect cannot be honestly asserted in jsdom (scrollTop/scrollHeight are 0
 * there, so a test asserting scrollTop === scrollHeight would be vacuously
 * green).
 */
export function shouldFollow(scrollTop: number, scrollHeight: number, clientHeight: number): boolean {
  return scrollHeight - scrollTop - clientHeight <= FOLLOW_EPSILON
}
