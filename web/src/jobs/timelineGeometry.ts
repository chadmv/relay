import type { Job } from './api'

export interface BarGeometry {
  /** Percentage of the axis, clamped to [0, 100]. */
  leftPct: number
  /** Percentage of the axis, clamped so left + width never exceeds 100. */
  widthPct: number
  /**
   * True when the bar has no duration to draw - a job that never started, or one
   * whose whole span falls outside the axis. The view renders a minimum-width
   * marker and SAYS so in the row text, because a small box on a gantt reads as a
   * short piece of work rather than as a point in time.
   */
  instant: boolean
}

function clamp(v: number): number {
  return Math.min(100, Math.max(0, v))
}

/**
 * Where one job's bar sits on the axis [sinceMs, untilMs).
 *
 * t0 is started_at, or created_at when the job never started. t1 is finished_at,
 * or the axis end for a job that started and has not finished, or t0 for one that
 * never started at all. Both ends are clamped, because the window bounds
 * created_at rather than activity: a job created inside the window can have
 * started before it or be running past it.
 *
 * A minimum VISIBLE width is not expressed here. It is a CSS pixel minimum on the
 * element, so a four-second job in a seven-day window is still visible without any
 * percentage arithmetic that could push a bar past the right edge.
 */
export function barGeometry(job: Job, sinceMs: number, untilMs: number): BarGeometry {
  const span = Math.max(1, untilMs - sinceMs)
  const startedMs = job.started_at ? new Date(job.started_at).getTime() : NaN
  const started = Number.isFinite(startedMs) ? startedMs : null

  const createdMs = new Date(job.created_at).getTime()
  const t0 = started ?? createdMs
  if (!Number.isFinite(t0)) return { leftPct: 0, widthPct: 0, instant: true }

  const finishedMs = job.finished_at ? new Date(job.finished_at).getTime() : NaN
  const t1 = Number.isFinite(finishedMs) ? finishedMs : started === null ? t0 : untilMs

  const leftPct = clamp(((t0 - sinceMs) / span) * 100)
  const rightPct = clamp(((t1 - sinceMs) / span) * 100)
  const widthPct = Math.max(0, rightPct - leftPct)
  return { leftPct, widthPct, instant: widthPct === 0 }
}
