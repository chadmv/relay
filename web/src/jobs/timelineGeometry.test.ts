import { expect, test } from 'vitest'
import { barGeometry } from './timelineGeometry'
import type { Job } from './api'

const SINCE = Date.UTC(2026, 8, 2, 0, 0, 0)
const UNTIL = Date.UTC(2026, 8, 3, 0, 0, 0)

// Hand-built rather than marshalled through anything: the fields under test are
// exactly the optional ones the server omits.
function job(over: Partial<Job>): Job {
  return {
    id: 'AAAAAA',
    name: 'j',
    priority: 'normal',
    status: 'pending',
    labels: null,
    created_at: new Date(Date.UTC(2026, 8, 2, 6, 0, 0)).toISOString(),
    updated_at: new Date(Date.UTC(2026, 8, 2, 6, 0, 0)).toISOString(),
    ...over,
  }
}

test('a job that never started is an instant at its creation time', () => {
  // t0 == t1 == created_at. A reader who sees a small box on a gantt reads it as
  // a short piece of work, so the row text says queued and this flag is what
  // lets the view say "not started" rather than draw a tiny duration.
  const g = barGeometry(job({}), SINCE, UNTIL)
  expect(g.leftPct).toBeCloseTo(25, 6)
  expect(g.widthPct).toBe(0)
  expect(g.instant).toBe(true)
})

test('a running job reaches the right edge', () => {
  const g = barGeometry(
    job({ status: 'running', started_at: new Date(Date.UTC(2026, 8, 2, 12, 0, 0)).toISOString() }),
    SINCE,
    UNTIL,
  )
  expect(g.leftPct).toBeCloseTo(50, 6)
  expect(g.leftPct + g.widthPct).toBeCloseTo(100, 6)
  expect(g.instant).toBe(false)
})

test('a finished job spans its start and its finish', () => {
  const g = barGeometry(
    job({
      status: 'done',
      started_at: new Date(Date.UTC(2026, 8, 2, 6, 0, 0)).toISOString(),
      finished_at: new Date(Date.UTC(2026, 8, 2, 18, 0, 0)).toISOString(),
    }),
    SINCE,
    UNTIL,
  )
  expect(g.leftPct).toBeCloseTo(25, 6)
  expect(g.widthPct).toBeCloseTo(50, 6)
})

test('a job that started before the axis is clamped to it', () => {
  // The window bounds created_at, not activity, so a job can legitimately have a
  // started_at outside the drawn axis. Without the clamp the bar's left is
  // negative and it escapes its own track.
  const g = barGeometry(
    job({
      status: 'done',
      created_at: new Date(Date.UTC(2026, 8, 2, 1, 0, 0)).toISOString(),
      started_at: new Date(Date.UTC(2026, 7, 20, 0, 0, 0)).toISOString(),
      finished_at: new Date(Date.UTC(2026, 8, 4, 0, 0, 0)).toISOString(),
    }),
    SINCE,
    UNTIL,
  )
  expect(g.leftPct).toBe(0)
  expect(g.widthPct).toBe(100)
})

test('an unparseable timestamp draws nothing rather than NaN', () => {
  // Not reachable from the Go server, which emits well-formed RFC3339, but a row
  // hand-edited by SQL or the CLI could carry garbage - and a NaN percentage in a
  // style attribute silently drops the rule instead of failing.
  const g = barGeometry(job({ created_at: 'not a date' }), SINCE, UNTIL)
  expect(g.leftPct).toBe(0)
  expect(g.widthPct).toBe(0)
  expect(g.instant).toBe(true)
})
