import { expect, test } from 'vitest'
import {
  appendEntries,
  createLogState,
  finalizePartials,
  visibleRows,
  MAX_LINES,
  FOLLOW_EPSILON,
  DROP_MARKER_TEXT,
  markDropped,
  prependEntries,
  shouldFollow,
  type LogChunk,
} from './logBuffer'

function chunk(seq: number, content: string, stream: 'stdout' | 'stderr' = 'stdout'): LogChunk {
  return { seq, stream, content, created_at: '2026-08-09T14:36:25.000Z' }
}

test('a fresh state is empty with maxSeq 0', () => {
  const s = createLogState()
  expect(s.lines).toEqual([])
  expect(s.maxSeq).toBe(0)
  expect(s.dropped).toBe(false)
  expect(s.evicted).toBe(false)
})

// Paired positive control on the same call path: one entry below maxSeq and one
// above, in the SAME appendEntries call. Feeding only distinct seqs would pass
// with the dedupe deleted.
test('discards entries at or below maxSeq and accepts those above, advancing maxSeq', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(10, 'first\n')])
  expect(s.maxSeq).toBe(10)

  s = appendEntries(s, [chunk(10, 'duplicate\n'), chunk(9, 'older\n'), chunk(11, 'newer\n')])
  expect(s.lines.map((l) => l.text)).toEqual(['first', 'newer'])
  expect(s.maxSeq).toBe(11)
})

test('returns the identical state object when every entry is a duplicate', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(5, 'a\n')])
  const before = s
  s = appendEntries(s, [chunk(5, 'a\n'), chunk(1, 'b\n')])
  // Reference equality, not deep equality: the hook relies on this to skip a
  // render when a replayed frame turns out to be a duplicate.
  expect(s).toBe(before)
})

// THE test that protects against the README's old, wrong contract. seq comes from
// a table-wide BIGSERIAL, so gaps are normal on a busy farm
// (README.md, "Events (Server-Sent Events)"); any gap detection would
// re-backfill on nearly every frame.
test('non-contiguous seq is NOT a drop signal', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(10, 'a\n'), chunk(40, 'b\n'), chunk(41, 'c\n')])
  expect(s.lines.map((l) => l.text)).toEqual(['a', 'b', 'c'])
  expect(s.dropped).toBe(false)
  expect(s.lines.every((l) => l.kind === 'line')).toBe(true)
  expect(s.maxSeq).toBe(41)
})

test('assigns a unique increasing render key to every row', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(1, 'a\nb\n'), chunk(2, 'c\n')])
  const keys = s.lines.map((l) => l.key)
  expect(new Set(keys).size).toBe(3)
  expect(keys).toEqual([...keys].sort((x, y) => x - y))
})

test('normalises an unexpected stream value to stdout', () => {
  let s = createLogState()
  s = appendEntries(s, [{ seq: 1, stream: 'weird', content: 'a\n', created_at: '' }])
  expect(s.lines[0].stream).toBe('stdout')
})

test('one entry containing three newlines yields three lines', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(1, 'one\ntwo\nthree\n')])
  expect(s.lines.map((l) => l.text)).toEqual(['one', 'two', 'three'])
})

// The reassembly test. Asserting only "the text appears" would pass against an
// implementation that renders one row per ENTRY, which is today's behaviour and
// exactly the defect being fixed - so assert the exact row COUNT.
test('a line split across two entries renders as ONE line', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(1, 'abc'), chunk(2, 'def\n')])
  expect(s.lines).toHaveLength(1)
  expect(s.lines[0].text).toBe('abcdef')
})

test('a dangling partial is a provisional row, not a completed line', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(1, 'done\nprompt> ')])
  expect(s.lines.map((l) => l.text)).toEqual(['done'])

  const rows = visibleRows(s)
  expect(rows).toHaveLength(2)
  expect(rows[1]).toMatchObject({ kind: 'partial', text: 'prompt> ' })
  // Provisional rows use negative keys so they can never collide with the
  // positive keys of retained lines.
  expect(rows[1].key).toBeLessThan(0)
})

test('finalizePartials flushes a dangling partial into a real line', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(1, 'no trailing newline')])
  expect(s.lines).toHaveLength(0)

  s = finalizePartials(s)
  expect(s.lines.map((l) => l.text)).toEqual(['no trailing newline'])
  expect(visibleRows(s).every((r) => r.kind === 'line')).toBe(true)
  // Idempotent: a second call must not duplicate the line.
  expect(finalizePartials(s)).toBe(s)
})

test('a carriage-return run collapses to the segment after the final CR', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(1, 'frame 1/100\rframe 2/100\rframe 3/100\n')])
  expect(s.lines.map((l) => l.text)).toEqual(['frame 3/100'])
})

// THE THREE DISCRIMINATING INPUTS (spec T1-A). All three must stay:
//   case 1 alone passes under a fix that deleted the progress-bar collapse;
//   case 2 alone passes at HEAD, where the bug lives;
//   case 3 alone passes under a fix that strips every CR, interior ones included.
//
// CASE 3 IS THE ONE THE BACKLOG ITEM'S OWN DESIGN FAILS. That item proposes
// stripping at most ONE trailing carriage return. The Windows C runtime writes
// "done\r\r\n" for print("done", end="\r") followed by print() - a literal \r is
// not translated, the \n becomes \r\n - so the line is "done\r\r" and a single
// strip still renders it blank. The spec refutes the item here (R1/D1). Do not
// narrow this rule back.
//
// Unlike the Go straddle test in internal/agent, each case here is an
// INDEPENDENT appendEntries call over a fresh state, so no case can pass by
// riding on a previous one and the table order is not load-bearing.
test('collapseCR strips EVERY trailing carriage return and still collapses interior CR runs', () => {
  const cases: Array<[string, string[]]> = [
    ['hello windows\r\nsecond line\r\n', ['hello windows', 'second line']],
    ['frame 1/100\rframe 2/100\rframe 3/100\n', ['frame 3/100']],
    ['x\r\r\n', ['x']],
  ]
  const got = cases.map(([content]) =>
    appendEntries(createLogState(), [chunk(1, content)]).lines.map((l) => l.text),
  )
  expect(got).toEqual(cases.map(([, want]) => want))
})

test('ANSI SGR escape sequences are stripped', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(1, '[32mgreen[0m and [1;31mred[0m\n')])
  expect(s.lines[0].text).toBe('green and red')
  expect(s.lines[0].text).not.toContain('')
  expect(s.lines[0].text).not.toContain('[32m')
})

test('an ANSI erase-line sequence is stripped too', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(1, '[2Kprogress\n')])
  expect(s.lines[0].text).toBe('progress')
})

test('stdout and stderr partials do not corrupt each other', () => {
  let s = createLogState()
  s = appendEntries(s, [
    chunk(1, 'out-a', 'stdout'),
    chunk(2, 'err-a', 'stderr'),
    chunk(3, 'out-b\n', 'stdout'),
    chunk(4, 'err-b\n', 'stderr'),
  ])
  expect(s.lines.map((l) => [l.stream, l.text])).toEqual([
    ['stdout', 'out-aout-b'],
    ['stderr', 'err-aerr-b'],
  ])
})

test('a completed line carries the created_at of the entry that terminated it', () => {
  let s = createLogState()
  s = appendEntries(s, [
    { seq: 1, stream: 'stdout', content: 'half', created_at: '2026-08-09T00:00:01.000Z' },
    { seq: 2, stream: 'stdout', content: 'done\n', created_at: '2026-08-09T00:00:02.000Z' },
  ])
  expect(s.lines[0].time).toBe('2026-08-09T00:00:02.000Z')
})

test('visibleRows returns the lines array itself when there is no partial', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(1, 'a\n')])
  expect(visibleRows(s)).toBe(s.lines)
})

// Non-vacuity: assert WHICH lines were retained, not just how many. A cap that
// kept the OLDEST MAX_LINES would pass a length-only assertion.
test('the line cap retains the newest MAX_LINES and flags eviction', () => {
  let s = createLogState()
  const entries: LogChunk[] = []
  for (let i = 1; i <= MAX_LINES + 50; i++) entries.push(chunk(i, `line-${i}\n`))
  s = appendEntries(s, entries)

  expect(s.lines).toHaveLength(MAX_LINES)
  expect(s.evicted).toBe(true)
  expect(s.lines[0].text).toBe('line-51')
  expect(s.lines[s.lines.length - 1].text).toBe(`line-${MAX_LINES + 50}`)
})

test('exactly MAX_LINES does not set the eviction flag', () => {
  let s = createLogState()
  const entries: LogChunk[] = []
  for (let i = 1; i <= MAX_LINES; i++) entries.push(chunk(i, `line-${i}\n`))
  s = appendEntries(s, entries)
  expect(s.lines).toHaveLength(MAX_LINES)
  expect(s.evicted).toBe(false)
})

test('markDropped appends a marker row and sets the dropped flag', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(1, 'before\n')])
  s = markDropped(s)
  s = appendEntries(s, [chunk(2, 'after\n')])

  expect(s.dropped).toBe(true)
  expect(s.lines.map((l) => [l.kind, l.text])).toEqual([
    ['line', 'before'],
    ['marker', DROP_MARKER_TEXT],
    ['line', 'after'],
  ])
})

// The marker is permanent for the session: once lines have been missed the view
// is no longer provably complete, so silence would misrepresent an incomplete log
// as complete. But it must not STACK: a real recovery cycle (Task 8's
// retry-exhaustion path, or the H2 fix's bounded drop-recovery path) can call
// markDropped many times in a row with no intervening line, and each one must
// be a no-op once the last retained row is already a marker - otherwise a
// normal retry exhaustion alone leaves 6 markers, and the review's 25-drop
// probe left 25.
test('markDropped is a no-op when the last retained row is already a marker', () => {
  const s = markDropped(markDropped(createLogState()))
  expect(s.lines.filter((l) => l.kind === 'marker')).toHaveLength(1)
  expect(s.dropped).toBe(true)
})

test('markDropped inserts a new marker again once a real line has been appended since the last one', () => {
  let s = markDropped(createLogState())
  s = appendEntries(s, [chunk(1, 'recovered\n')])
  s = markDropped(s)
  expect(s.lines.map((l) => [l.kind, l.text])).toEqual([
    ['marker', DROP_MARKER_TEXT],
    ['line', 'recovered'],
    ['marker', DROP_MARKER_TEXT],
  ])
  expect(s.dropped).toBe(true)
})

test('shouldFollow is true at the bottom and just inside the epsilon', () => {
  expect(shouldFollow(1000, 2000, 1000)).toBe(true) // exactly at the bottom
  expect(shouldFollow(1000 - FOLLOW_EPSILON, 2000, 1000)).toBe(true) // exactly at epsilon
  expect(shouldFollow(1000 - (FOLLOW_EPSILON - 1), 2000, 1000)).toBe(true)
})

test('shouldFollow is false once scrolled further than the epsilon off the bottom', () => {
  expect(shouldFollow(1000 - (FOLLOW_EPSILON + 1), 2000, 1000)).toBe(false)
  expect(shouldFollow(0, 2000, 1000)).toBe(false)
})

test('shouldFollow is true for a container smaller than its viewport', () => {
  // jsdom reports 0/0/0 for an unlaid-out element; that must not read as
  // "scrolled away", or follow-tail would switch itself off on mount.
  expect(shouldFollow(0, 0, 0)).toBe(true)
})

// The IN-FLIGHT PARTIAL paths (spec T1-C), which are two of collapseCR's four
// call sites and are NOT reached by the line split. A chunk boundary landing
// between the \r and the \n leaves "text\r" in the partial buffer, so the live
// tail flickers blank and then fills in; a task whose final line has no trailing
// newline blanks that line permanently.
//
// BOTH STREAMS ON PURPOSE: visibleRows renders the stdout and stderr partials
// through two SEPARATE call sites (the two collapseCR calls inside visibleRows,
// one per stream), so a stdout-only fixture passes against a fix applied to
// one of them.
test('a partial ending in a carriage return renders its text, on stdout and on stderr', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(1, 'text\r', 'stdout'), chunk(2, 'oops\r', 'stderr')])

  expect(visibleRows(s).map((r) => [r.stream, r.kind, r.text])).toEqual([
    ['stdout', 'partial', 'text'],
    ['stderr', 'partial', 'oops'],
  ])

  const f = finalizePartials(s)
  expect(f.lines.map((l) => [l.stream, l.text])).toEqual([
    ['stdout', 'text'],
    ['stderr', 'oops'],
  ])
})

// Spec T1-D. GREEN AT HEAD AND GREEN AFTER THE FIX - a non-regression pin, not a
// RED test, and saying so is the point: an empty line is CONTENT, not an
// absence, and a fix that started dropping rows whose text collapses to '' would
// silently delete blank lines from the log. Mutation M3b is its kill.
test('an empty or CR-only unit still renders a row', () => {
  expect(appendEntries(createLogState(), [chunk(1, '\r\n')]).lines.map((l) => l.text)).toEqual([''])
  expect(appendEntries(createLogState(), [chunk(1, '\n\n')]).lines.map((l) => l.text)).toEqual(['', ''])
  const dangling = appendEntries(createLogState(), [chunk(1, '\r')])
  expect(visibleRows(dangling).map((r) => r.text)).toEqual([''])
})

// Spec T1-E: an erase-line escape sequence sitting BETWEEN the carriage return
// and the newline - which is exactly what a progress bar emits. stripAnsi runs
// before the split, inside appendEntries, so the sequence is gone by the time
// the line is formed and the CR it was hiding behind is trailing after all.
//
// Without this test nothing pins that the strip runs AFTER stripAnsi: a strip
// applied to e.content would see '\n' in the final position here and remove
// nothing. The ESC byte is written as an escape rather than literally so this
// test survives being copied out of a plan document.
test('a carriage return revealed by stripping an ANSI erase-line sequence is still stripped', () => {
  const ESC = '\u001B'
  const s = appendEntries(createLogState(), [chunk(1, 'text\r' + ESC + '[K\n')])
  expect(s.lines.map((l) => l.text)).toEqual(['text'])
})

// Spec T3-B, the render-invisibility claim (spec 4.4) made executable. The whole
// reason Part 2 (the agent's CRLF collapse) can ship in the same slice without
// changing what the SPA shows is that an UPGRADED agent's bytes and an
// UN-UPGRADED agent's bytes render identically here. Under a strip-ONE rule they
// would not, which is a second, independent reason not to narrow the rule.
test('an upgraded and an un-upgraded agent render the same line', () => {
  const upgraded = appendEntries(createLogState(), [chunk(1, 'x\r\n')])
  const notUpgraded = appendEntries(createLogState(), [chunk(1, 'x\r\r\n')])
  expect(upgraded.lines.map((l) => l.text)).toEqual(['x'])
  expect(notUpgraded.lines.map((l) => l.text)).toEqual(['x'])
})

test('minSeq records the lowest seq ever accepted and never rises', () => {
  let s = createLogState()
  expect(s.minSeq).toBe(0)

  s = appendEntries(s, [chunk(10, 'a\n'), chunk(40, 'b\n')])
  // The FIRST accepted entry, not the last, and not the smallest of a later
  // batch: this is the cursor a backwards fetch continues from.
  expect(s.minSeq).toBe(10)
  expect(s.maxSeq).toBe(40)

  s = appendEntries(s, [chunk(41, 'c\n')])
  expect(s.minSeq).toBe(10)
  expect(s.maxSeq).toBe(41)

  // A duplicate below maxSeq is discarded before it can move anything.
  s = appendEntries(s, [chunk(5, 'old\n')])
  expect(s.minSeq).toBe(10)
})

test('prependEntries joins a line that straddles the seam', () => {
  // The window starts mid-line: the tail page began at 'World\n', so its first
  // row is the text from the window's start to that stream's first newline.
  let s = createLogState()
  s = appendEntries(s, [chunk(10, 'World\nsecond\n')])
  expect(s.lines.map((l) => l.text)).toEqual(['World', 'second'])

  // The earlier page ends mid-line with 'Hello ' dangling.
  s = prependEntries(s, [chunk(8, 'zero\n'), chunk(9, 'one\nHello ')])

  // Exact text AND exact count: an implementation that appends the fragment as
  // its own row produces four rows that read almost the same.
  expect(s.lines.map((l) => l.text)).toEqual(['zero', 'one', 'Hello World', 'second'])
  expect(s.lines).toHaveLength(4)
})

test('prependEntries keeps the two streams seams apart', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(20, 'out-tail\n'), chunk(21, 'err-tail\n', 'stderr')])
  s = prependEntries(s, [chunk(10, 'out-head ', 'stdout'), chunk(11, 'err-head ', 'stderr')])

  const out = s.lines.filter((l) => l.stream === 'stdout').map((l) => l.text)
  const err = s.lines.filter((l) => l.stream === 'stderr').map((l) => l.text)
  expect(out).toEqual(['out-head out-tail'])
  expect(err).toEqual(['err-head err-tail'])
})

test('prependEntries does not join into a marker row', () => {
  // The reachable shape: a drop happens before any line arrives, so markDropped
  // puts a marker at index 0 and the tail page's lines follow it. markDropped
  // emits its marker with stream 'stdout' REGARDLESS of which stream dropped,
  // so a naive "first row of this stream" scan joins into the marker text.
  let s = createLogState()
  s = markDropped(s)
  s = appendEntries(s, [chunk(20, 'World\n')])
  expect(s.lines[0].kind).toBe('marker')

  s = prependEntries(s, [chunk(10, 'Hello ')])

  expect(s.lines[0].kind).toBe('marker')
  expect(s.lines[0].text).toBe(DROP_MARKER_TEXT)
  expect(s.lines.map((l) => l.text)).toEqual([DROP_MARKER_TEXT, 'Hello World'])
})

test('prependEntries refuses after eviction', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(1, 'x\n'.repeat(MAX_LINES + 1))])
  expect(s.evicted).toBe(true)
  const before = s
  s = prependEntries(s, [chunk(1, 'earlier\n')])
  // Reference equality: once drop-oldest has evicted the front of the window,
  // the first row is no longer the continuation of minSeq, so there is no seam
  // to join to and the control that produced this call is disabled anyway.
  expect(s).toBe(before)
})

test('prependEntries that overflows the cap keeps the newest lines', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(1000, 'keep-me\n')])
  const batch = Array.from({ length: MAX_LINES + 5 }, (_, i) => chunk(i + 1, `old-${i}\n`))
  s = prependEntries(s, batch)

  expect(s.lines).toHaveLength(MAX_LINES)
  expect(s.evicted).toBe(true)
  // The FIRST retained line, not the length: keeping the OLDEST lines would
  // also produce MAX_LINES rows and would drop the live tail.
  expect(s.lines[0].text).toBe('old-6')
  expect(s.lines[s.lines.length - 1].text).toBe('keep-me')
})

test('prependEntries lowers minSeq only', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(100, 'a\n')])
  s = prependEntries(s, [chunk(40, 'b\n'), chunk(50, 'c\n')])
  expect(s.minSeq).toBe(40)
  expect(s.maxSeq).toBe(100)
})

test('prependEntries assigns fresh keys that collide with nothing', () => {
  let s = createLogState()
  s = appendEntries(s, [chunk(100, 'a\nb\n')])
  s = prependEntries(s, [chunk(40, 'c\nd\n')])
  const keys = s.lines.map((l) => l.key)
  expect(new Set(keys).size).toBe(keys.length)
})
