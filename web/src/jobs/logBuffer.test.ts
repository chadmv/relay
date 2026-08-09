import { expect, test } from 'vitest'
import { appendEntries, createLogState, type LogChunk } from './logBuffer'

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
  // Task 3 tightens this to an exact 3 once entries are reassembled into lines.
  expect(new Set(keys).size).toBe(s.lines.length)
  expect(keys).toEqual([...keys].sort((x, y) => x - y))
})

test('normalises an unexpected stream value to stdout', () => {
  let s = createLogState()
  s = appendEntries(s, [{ seq: 1, stream: 'weird', content: 'a\n', created_at: '' }])
  expect(s.lines[0].stream).toBe('stdout')
})
