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
// (README.md:1357-1360); any gap detection would re-backfill on nearly every
// frame.
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
