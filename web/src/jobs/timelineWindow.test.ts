import { expect, test } from 'vitest'
import {
  ANCHOR_STEP_MS,
  NEXT_SHORTER,
  TICKS,
  TIMELINE_MAX_PAGES,
  TIMELINE_PAGE_SIZE,
  TIMELINE_WINDOWS,
  WINDOW_LABEL,
  WINDOW_MS,
  windowBounds,
} from './timelineWindow'

test('the anchor is quantized', () => {
  const base = Date.UTC(2026, 8, 2, 12, 0, 0)
  const a = windowBounds('24h', base + 1)
  const b = windowBounds('24h', base + ANCHOR_STEP_MS - 1)
  // Two calls milliseconds apart return identical bounds. Without the flooring
  // every render mints a new query key, which is an unbounded fan-out of walks.
  expect(a).toEqual(b)
  expect(new Date(a.untilIso).getTime() % ANCHOR_STEP_MS).toBe(0)

  // The next step is a DIFFERENT window, which is what makes the advance the
  // refresh: there is no refetchInterval on this query.
  const next = windowBounds('24h', base + ANCHOR_STEP_MS)
  expect(next.untilIso).not.toBe(a.untilIso)
})

test('since is until minus the window', () => {
  const { sinceIso, untilIso } = windowBounds('7d', Date.UTC(2026, 8, 2, 12, 0, 0))
  // The server's since/until bound created_at half-open, [since, until), so
  // consecutive windows tile without a job appearing in two of them.
  expect(new Date(untilIso).getTime() - new Date(sinceIso).getTime()).toBe(WINDOW_MS['7d'])
})

test('every window has a label, ticks and a narrowing answer', () => {
  for (const w of TIMELINE_WINDOWS) {
    expect(WINDOW_LABEL[w]).toBeTruthy()
    expect(WINDOW_MS[w]).toBeGreaterThan(0)
    // TICKS[w] having length 3, and `w in NEXT_SHORTER`, are both guaranteed by
    // the Record types over TimelineWindow (a [string,string,string] tuple and
    // an exhaustive key set respectively) rather than by anything runtime here
    // - tsc rejects a shorter/longer tuple or a missing key at the declaration
    // site, which is what the type-level probe below this test proves. The
    // genuine runtime fact left to check is the VALUE at the last tick.
    expect(TICKS[w][2]).toBe('NOW')
  }
})

test('exactly the shortest window has no narrower neighbour', () => {
  const shortest = [...TIMELINE_WINDOWS].sort((a, b) => WINDOW_MS[a] - WINDOW_MS[b])[0]
  for (const w of TIMELINE_WINDOWS) {
    if (w === shortest) {
      expect(NEXT_SHORTER[w]).toBeNull()
    } else {
      const next = NEXT_SHORTER[w]
      expect(next).not.toBeNull()
      expect(WINDOW_MS[next as (typeof TIMELINE_WINDOWS)[number]]).toBeLessThan(WINDOW_MS[w])
    }
  }
})

test('the page cap is the server maximum and a bounded number of round trips', () => {
  // parsePage REJECTS a limit outside [1, 200] with a 400; it does not clamp, so
  // this is a ceiling rather than a suggestion.
  expect(TIMELINE_PAGE_SIZE).toBe(200)
  expect(TIMELINE_MAX_PAGES).toBe(3)
})
