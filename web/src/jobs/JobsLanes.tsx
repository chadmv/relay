import { Link } from 'react-router-dom'
import { Button } from '../components/Button'
import { GlassPanel, ProgressBar } from '../components/holo'
import type { JobStatus } from './api'
import { LANE_LABELS } from './lanes'
import { progressPct, statusColor } from './status'
import type { LaneState } from './useJobLanes'

// One horizontal scroll region holding every lane. tabIndex + role="group" follow
// components/holo/Table.tsx: a scroll container with no focusable descendant is an
// axe scrollable-region-focusable violation and WebKit grants no implicit scroller
// focusability. With every lane empty there is no focusable descendant at all,
// which is that case. role="group" is not a landmark; it exists so aria-label has
// a role to attach a name to.
const SCROLLER = 'min-w-0 overflow-x-auto'
const ROW = 'flex gap-3 pb-2'
// Fixed width, no grow and no shrink: the lanes are together wider than any
// viewport this app is tested at, so the row scrolls inside SCROLLER instead of
// widening the document. Same width at 320 as at 1280 - no breakpoint, because
// stacking would nest a vertical scroller inside a vertical scroller per lane and
// would mean the widths measured at 320 are not the widths shipped at 1280.
const LANE = 'flex w-[280px] shrink-0 flex-col gap-2 p-3'
// Capped height so a full lane scrolls within itself rather than stretching the page.
const LANE_BODY = 'flex max-h-[520px] flex-col gap-2 overflow-y-auto'
const CARD = 'block rounded-[8px] border border-border bg-white/[0.04] p-2.5 hover:border-accent/60'

export function JobsLanes({
  lanes,
  onShowAll,
}: {
  lanes: LaneState[]
  onShowAll: (status: JobStatus) => void
}) {
  return (
    <div className={SCROLLER} tabIndex={0} role="group" aria-label="Job lanes, scrolls horizontally">
      <div className={ROW}>
        {lanes.map((lane) => (
          <JobLane key={lane.status} lane={lane} onShowAll={onShowAll} />
        ))}
      </div>
    </div>
  )
}

function JobLane({
  lane,
  onShowAll,
}: {
  lane: LaneState
  onShowAll: (status: JobStatus) => void
}) {
  const headingId = `lane-${lane.status}`
  const c = statusColor(lane.status)
  const hidden = lane.total - lane.items.length
  return (
    <GlassPanel as="section" role="region" aria-labelledby={headingId} className={LANE}>
      {/* The header renders in every state, so a lane never disappears from the
          row and the column count is constant. */}
      <div className="flex items-center justify-between gap-2 px-1">
        <div className="flex items-center gap-2">
          <span className={`h-1.5 w-1.5 rounded-full ${c.dot}`} aria-hidden="true" />
          <h2 id={headingId} className="font-mono text-[10.5px] uppercase tracking-[0.18em] text-fg-mute">
            {LANE_LABELS[lane.status]}
          </h2>
        </div>
        {/* "total" is spelled out because the KPI strip's DONE-24H sits just above
            and is a different, 24-hour-scoped number. This one is all-time. */}
        <span className={`flex-none font-mono text-[11px] ${c.text}`}>
          {lane.total.toLocaleString()} total
        </span>
      </div>

      {lane.isLoading ? (
        <div className={LANE_BODY}>
          {Array.from({ length: 3 }).map((_, i) => (
            <GlassPanel key={i} className="h-14" />
          ))}
        </div>
      ) : lane.error ? (
        <div className="flex flex-col items-start gap-2 px-1 text-[12px] text-err">
          {lane.error.message}
          <Button className="w-auto px-3" onClick={() => lane.refetch()}>
            Retry
          </Button>
        </div>
      ) : lane.items.length === 0 ? (
        <div className="px-1 py-4 text-[12px] text-fg-mute">No jobs</div>
      ) : (
        <ul className={LANE_BODY}>
          {lane.items.map((j) => {
            const pct = progressPct(j.done_tasks, j.total_tasks)
            return (
              <li key={j.id}>
                <Link to={`/jobs/${j.id}`} className={CARD}>
                  <span className="block truncate text-[12px] text-fg">{j.name}</span>
                  <ProgressBar className="my-1.5" value={pct} />
                  <span className="font-mono text-[10px] text-fg-mute">
                    {j.done_tasks ?? 0}/{j.total_tasks ?? 0} tasks, {pct}%
                  </span>
                </Link>
              </li>
            )
          })}
        </ul>
      )}

      {hidden > 0 && (
        <button
          type="button"
          onClick={() => onShowAll(lane.status)}
          className="rounded-[8px] border border-dashed border-border px-3 py-2 font-mono text-[11px] text-fg-mute hover:text-fg"
        >
          + {hidden.toLocaleString()} more
        </button>
      )}
    </GlassPanel>
  )
}
