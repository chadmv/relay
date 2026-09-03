import type { ScheduleStats } from './api'

// FOUR TILES, and the fourth is the deviation from the hi-fi worth defending.
// `failing` is the only fleet-wide answer to "is anything broken right now" - the
// row-level FAILING chip is page-scoped, so on page 3 of a filtered list it cannot
// answer that - and it counts a failure class that is invisible to
// failed_runs_24h by construction, because a spawn failure never becomes a job.
//
// THE LABELS NAME THEIR UNITS. failed_runs_24h is over jobs, windowed, and
// includes run-now jobs; failing is over schedules and is not windowed. Two
// adjacent labels both reading FAILED would invite the reading that one is a
// subset of the other.
//
// Colour is never the sole carrier: every number has its own label beside it, and
// the two failure numbers take different tokens only so they are also
// distinguishable without reading.
const TILES = [
  { key: 'enabled', label: 'ENABLED', tone: 'text-ok' },
  { key: 'paused', label: 'PAUSED', tone: 'text-fg' },
  { key: 'failed_runs_24h', label: 'FAILED RUNS 24H', tone: 'text-err' },
  { key: 'failing', label: 'FAILING SCHEDULES', tone: 'text-warn' },
] as const

// A HYPHEN, NOT A ZERO, until the first response. A zero is a fact this component
// does not have yet, and the reassuring one; and a tile that disappears instead
// changes the strip's width mid-measure and reads as a missing feature. The
// ternary is on `stats` itself rather than on each value, so a real zero from the
// server still renders as a zero - the response carries no omitempty, so a zero is
// a zero and never an absence.
export function SchedulesSummary({
  stats,
  statsFailed,
  filterActive,
}: {
  stats?: ScheduleStats
  statsFailed: boolean
  filterActive: boolean
}) {
  return (
    <div className="flex flex-wrap items-center gap-4 font-mono text-[11px] tracking-[0.14em] text-fg-mute">
      {TILES.map((t) => (
        <span key={t.key} data-testid={`schedules-stat-${t.key}`}>
          <b className={`${t.tone} text-[18px] font-semibold`}>{stats ? stats[t.key] : '-'}</b>{' '}
          {t.label}
        </span>
      ))}
      {/* The total lives here, not on the chips: /stats accepts no filters, so this
          number ignores q. The parenthetical appears at the exact moment it can
          disagree with the footer's filtered total, and answers the question where
          it is asked. */}
      <span className="text-fg-dim" data-testid="schedules-stat-total">
        {stats ? stats.total : '-'} SCHEDULES TOTAL{filterActive ? ' (UNFILTERED)' : ''}
      </span>
      {statsFailed && !stats ? <span className="text-fg-mute">counts unavailable</span> : null}
    </div>
  )
}
