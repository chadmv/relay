import { expect, test } from 'vitest'
import { createSseParser, type SseFrame } from './sse'

const TWO_FRAMES =
  'event: task_log\ndata: {"seq":1,"content":"a"}\n\n' +
  'event: dropped\ndata: {"reason":"slow_consumer"}\n\n'

const EXPECTED: SseFrame[] = [
  { event: 'task_log', data: '{"seq":1,"content":"a"}' },
  { event: 'dropped', data: '{"reason":"slow_consumer"}' },
]

test('parses whole frames from a single chunk', () => {
  const p = createSseParser()
  expect(p.push(TWO_FRAMES)).toEqual(EXPECTED)
})

// Non-vacuity: split the SAME payload at EVERY byte offset and demand the same
// result from all of them. A parser that only handles whole frames passes the
// test above and fails at most of the 90-odd offsets here.
test('parses the same two frames no matter where the chunk boundary falls', () => {
  for (let i = 1; i < TWO_FRAMES.length; i++) {
    const p = createSseParser()
    const frames = [...p.push(TWO_FRAMES.slice(0, i)), ...p.push(TWO_FRAMES.slice(i))]
    expect(frames, `split at offset ${i}`).toEqual(EXPECTED)
  }
})

test('emits nothing until a frame is terminated by a blank line', () => {
  const p = createSseParser()
  expect(p.push('event: task_log\ndata: {"seq":1}\n')).toEqual([])
  expect(p.push('\n')).toEqual([{ event: 'task_log', data: '{"seq":1}' }])
})

test('joins multi-line data with a newline', () => {
  const p = createSseParser()
  expect(p.push('event: x\ndata: one\ndata: two\n\n')).toEqual([{ event: 'x', data: 'one\ntwo' }])
})

test('parses CRLF line endings, including a CRLF split across chunks', () => {
  const p = createSseParser()
  expect(p.push('event: x\r\ndata: hi\r')).toEqual([])
  expect(p.push('\n\r\n')).toEqual([{ event: 'x', data: 'hi' }])
})

test('ignores comment (keepalive) lines without emitting a frame', () => {
  const p = createSseParser()
  expect(p.push(':keepalive\n\n')).toEqual([])
  expect(p.push('event: x\ndata: 1\n\n')).toEqual([{ event: 'x', data: '1' }])
})

test('surfaces an unknown event type rather than dropping it', () => {
  const p = createSseParser()
  expect(p.push('event: brand_new\ndata: {}\n\n')).toEqual([{ event: 'brand_new', data: '{}' }])
})

test('defaults a frame with no event field to "message"', () => {
  const p = createSseParser()
  expect(p.push('data: bare\n\n')).toEqual([{ event: 'message', data: 'bare' }])
})

test('accepts data with no space after the colon', () => {
  const p = createSseParser()
  expect(p.push('event:x\ndata:{"a":1}\n\n')).toEqual([{ event: 'x', data: '{"a":1}' }])
})

test('keeps a colon inside a data value', () => {
  const p = createSseParser()
  expect(p.push('data: {"url":"http://x/y"}\n\n')).toEqual([
    { event: 'message', data: '{"url":"http://x/y"}' },
  ])
})
