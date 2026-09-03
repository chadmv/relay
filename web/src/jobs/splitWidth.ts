// The left pane's share of the job detail body, as an integer percentage.
//
// PERCENT, not the hi-fi's pixels, and the LEFT pane, not its right one. The
// separator advertises aria-valuemin and aria-valuemax, and those are only
// meaningful with aria-valuenow if the range is stable: a pixel maximum is the
// container width minus a constant, so it moves on every window resize, and a
// persisted pixel value restored into a narrower window is outside its own
// announced range. A percent range is the same two numbers always. Sizing the
// left pane means one number to persist, one number to announce, and no second
// number that can disagree with the first.
//
// 55 is today's ratio, so a reader with nothing persisted sees the layout that
// was there before.
export const SPLIT_MIN = 30
export const SPLIT_MAX = 70
export const SPLIT_DEFAULT = 55
export const SPLIT_STEP = 2
export const SPLIT_STORAGE_KEY = 'relay.jobs.detailSplit'

export function clampSplit(pct: number): number {
  if (!Number.isFinite(pct)) return SPLIT_DEFAULT
  return Math.min(SPLIT_MAX, Math.max(SPLIT_MIN, Math.round(pct)))
}

// Anything another tab, a previous release or a hand-edited storage value can
// hold must degrade to the default. An out-of-range value restored straight into
// aria-valuenow would announce a value outside the range beside it.
export function parseStoredSplit(raw: string | null): number {
  if (raw === null) return SPLIT_DEFAULT
  const n = Number(raw)
  if (!Number.isInteger(n)) return SPLIT_DEFAULT
  if (n < SPLIT_MIN || n > SPLIT_MAX) return SPLIT_DEFAULT
  return n
}

// The rect is a parameter, not a measurement taken here: jsdom performs no
// layout, so every getBoundingClientRect there is zero and a function that
// measured internally would be untestable in the lane that owns this arithmetic.
export function splitFromPointer(clientX: number, rect: { left: number; width: number }): number {
  if (rect.width <= 0) return SPLIT_DEFAULT
  return clampSplit(((clientX - rect.left) / rect.width) * 100)
}
