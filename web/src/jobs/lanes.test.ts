import { expect, test } from 'vitest'
import { JOB_STATUSES } from './api'
import { LANE_CHIP_KEY, LANE_LABELS, LANE_LIMIT, LANE_ORDER } from './lanes'

test('lane order is exactly the job status vocabulary, in lifecycle order', () => {
  expect([...LANE_ORDER]).toEqual(['pending', 'running', 'done', 'failed', 'cancelled'])
  // Set equality with the vocabulary, checked separately from the order above: a
  // status with no lane is invisible in this view, and a lane for a status the
  // server cannot return is a permanently empty column.
  expect([...LANE_ORDER].sort()).toEqual([...JOB_STATUSES].sort())
})

test('every job status has a lane label and a chip key', () => {
  for (const status of JOB_STATUSES) {
    expect(LANE_LABELS[status]).toBeTruthy()
    expect(LANE_CHIP_KEY[status]).toBeTruthy()
  }
  // The pending lane is labelled Queued, matching the table's own chip. One page
  // must not call one state two things.
  expect(LANE_LABELS.pending).toBe('Queued')
})

test('the per-lane cap is inside the range the server accepts', () => {
  // parsePage REJECTS an out-of-range limit with a 400; it does not clamp.
  expect(LANE_LIMIT).toBeGreaterThanOrEqual(1)
  expect(LANE_LIMIT).toBeLessThanOrEqual(200)
})
