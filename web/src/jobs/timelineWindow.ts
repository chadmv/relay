// The window vocabulary and all of the timeline's time arithmetic. Pure, no React,
// so the geometry and the hook can each be driven without mounting anything.
//
// TIMELINE_WINDOWS is the single source: TimelineWindow derives from it, and every
// per-window fact below is a Record over that type, so adding a window without
// answering all four questions is a tsc error rather than a runtime hole.
export const TIMELINE_WINDOWS = ['6h', '24h', '7d'] as const

export type TimelineWindow = (typeof TIMELINE_WINDOWS)[number]

const HOUR_MS = 60 * 60 * 1000

export const WINDOW_MS: Record<TimelineWindow, number> = {
  '6h': 6 * HOUR_MS,
  '24h': 24 * HOUR_MS,
  '7d': 7 * 24 * HOUR_MS,
}

// Prose, for a sentence a screen reader gets read aloud. The picker's own buttons
// show the compact key instead.
export const WINDOW_LABEL: Record<TimelineWindow, string> = {
  '6h': '6 hours',
  '24h': '24 hours',
  '7d': '7 days',
}

// null means there is nothing narrower to offer, which is the branch the
// truncation affordance has to fall back to the table for.
export const NEXT_SHORTER: Record<TimelineWindow, TimelineWindow | null> = {
  '6h': null,
  '24h': '6h',
  '7d': '24h',
}

// Start, midpoint, end. Relative at every window: absolute wall-clock labels at
// fixed quarter positions are correct only for a calendar-aligned window, and this
// one is rolling and ends at the anchor.
export const TICKS: Record<TimelineWindow, readonly [string, string, string]> = {
  '6h': ['-6H', '-3H', 'NOW'],
  '24h': ['-24H', '-12H', 'NOW'],
  '7d': ['-7D', '-3.5D', 'NOW'],
}

/**
 * How coarsely the axis end is quantized, and therefore how often the query key
 * moves. It is the whole refresh cadence and the whole staleness budget of this
 * view in one number: a job created less than this long ago is not yet inside the
 * window. The panel states the axis end as a wall-clock time so that is visible
 * rather than implicit.
 */
export const ANCHOR_STEP_MS = 15_000

/** The server's maximum ?limit=. Above it the request is a 400, not a clamp. */
export const TIMELINE_PAGE_SIZE = 200

/** Three sequential round trips, so first paint has a bounded latency. */
export const TIMELINE_MAX_PAGES = 3

/**
 * The half-open axis [since, until) for one window, quantized to ANCHOR_STEP_MS.
 *
 * `until` is sent, not omitted. Letting the query run open-ended shows the newest
 * jobs a few seconds sooner and corrupts everything downstream: rows arrive that
 * fall outside the drawn axis, and `total` then counts rows the chart does not
 * draw - which is the input to the truncation banner.
 */
export function windowBounds(
  w: TimelineWindow,
  nowMs: number,
): { sinceIso: string; untilIso: string } {
  const anchor = Math.floor(nowMs / ANCHOR_STEP_MS) * ANCHOR_STEP_MS
  return {
    sinceIso: new Date(anchor - WINDOW_MS[w]).toISOString(),
    untilIso: new Date(anchor).toISOString(),
  }
}
