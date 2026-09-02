import type { JobStatus } from './api'

// Cards fetched per lane.
export const LANE_LIMIT = 10

// Left-to-right lane order. A presentation choice, so it is listed rather than
// derived - lanes.test.ts pins it against JOB_STATUSES as a set.
export const LANE_ORDER: readonly JobStatus[] = ['pending', 'running', 'done', 'failed', 'cancelled']

// Record<JobStatus, string>, not a partial map: adding a status to JOB_STATUSES
// without a label here is a tsc error rather than a lane that silently disappears.
export const LANE_LABELS: Record<JobStatus, string> = {
  pending: 'Queued',
  running: 'Running',
  done: 'Done',
  failed: 'Failed',
  cancelled: 'Cancelled',
}

// The table chip a lane's overflow control selects. Routing a key that is not in
// FILTERS would make the status lookup fall back to an empty status and show EVERY
// job while the chip row looks filtered - a wrong answer, not a missing one. The
// JobsPage.lanes.test.tsx guard 'every lane chip key names a real table filter for
// its own status' is what reddens for that.
export const LANE_CHIP_KEY: Record<JobStatus, string> = {
  pending: 'queued',
  running: 'running',
  done: 'done',
  failed: 'failed',
  cancelled: 'cancelled',
}
