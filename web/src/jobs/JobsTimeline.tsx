import { Link } from 'react-router-dom'
import { Button } from '../components/Button'
import { GlassPanel } from '../components/holo'
import { formatDateTime } from '../lib/time'
import { barGeometry } from './timelineGeometry'
import { formatDuration, progressPct, statusColor } from './status'
import { NEXT_SHORTER, TICKS, WINDOW_LABEL, type TimelineWindow } from './timelineWindow'
import type { TimelineState } from './useJobTimeline'

// The name column: a fixed track with a zero minimum, so the row is one grid at
// every width and nothing here can widen the document.
const ROW = 'grid grid-cols-[9rem_1fr] items-center gap-3 border-b border-accent/10 py-1.5'

export function JobsTimeline({
  state,
  window: w,
  filtering,
  onChooseWindow,
  onOpenTable,
}: {
  state: TimelineState
  window: TimelineWindow
  filtering: boolean
  onChooseWindow: (w: TimelineWindow) => void
  onOpenTable: () => void
}) {
  const sinceMs = new Date(state.sinceIso).getTime()
  const untilMs = new Date(state.untilIso).getTime()
  const shorter = NEXT_SHORTER[w]

  return (
    <GlassPanel as="section" aria-label="Jobs timeline" className="flex flex-col overflow-hidden">
      <div className="border-b border-border px-5 py-3.5">
        {/* CREATED, not "the last N hours". since/until bound created_at, so a job
            that started before the window is absent however long it has run. */}
        <p className="text-[13px] text-fg">
          Timeline - {state.jobs.length.toLocaleString()} of {state.total.toLocaleString()} jobs
          created in the last <span className="text-accent">{WINDOW_LABEL[w]}</span>, newest first.
          Axis {formatDateTime(state.sinceIso)} to {formatDateTime(state.untilIso)}.
        </p>
      </div>

      {state.truncated && (
        <div className="flex flex-wrap items-center gap-3 border-b border-border px-5 py-3 text-[12px] text-warn">
          <span>
            Showing the {state.jobs.length.toLocaleString()} most recent of{' '}
            {state.total.toLocaleString()} jobs created in the last {WINDOW_LABEL[w]}.
          </span>
          {shorter ? (
            <Button className="w-auto px-3" onClick={() => onChooseWindow(shorter)}>
              Show last {WINDOW_LABEL[shorter]}
            </Button>
          ) : (
            // No narrowing left. The paged table is the only surface that can show
            // all of them.
            <Button className="w-auto px-3" onClick={onOpenTable}>
              Open the Table view
            </Button>
          )}
        </div>
      )}

      {state.error ? (
        <div className="flex flex-col items-start gap-2 px-5 py-6 text-[12px] text-err">
          {state.error.message}
          <Button className="w-auto px-3" onClick={state.refetch}>
            Retry
          </Button>
        </div>
      ) : state.isLoading ? (
        <div className="flex flex-col gap-2 px-5 py-4">
          {Array.from({ length: 6 }).map((_, i) => (
            <GlassPanel key={i} className="h-6" />
          ))}
        </div>
      ) : state.jobs.length === 0 ? (
        <div className="px-5 py-8 text-center text-[13px] text-fg-mute">
          {filtering
            ? `No jobs match those filters in the last ${WINDOW_LABEL[w]}.`
            : `No jobs were created in the last ${WINDOW_LABEL[w]}.`}
        </div>
      ) : (
        <>
          {/* A bare sequence of relative offsets read aloud is noise, and the same
              information is in the paragraph above. */}
          <div aria-hidden="true" className={`${ROW} border-none px-5 pt-2.5`}>
            <span />
            <div className="relative h-4 border-b border-border">
              {TICKS[w].map((t, i) => (
                <span
                  key={t}
                  style={{
                    left: `${(i / (TICKS[w].length - 1)) * 100}%`,
                    transform:
                      i === TICKS[w].length - 1
                        ? 'translateX(-100%)'
                        : i === 0
                          ? 'none'
                          : 'translateX(-50%)',
                  }}
                  className={`absolute top-0 font-mono text-[9.5px] tracking-[0.14em] ${
                    t === 'NOW' ? 'text-accent' : 'text-fg-mute'
                  }`}
                >
                  {t}
                </span>
              ))}
            </div>
          </div>

          <ul className="px-5 pb-4">
            {state.jobs.map((j) => {
              const g = barGeometry(j, sinceMs, untilMs)
              const c = statusColor(j.status)
              const pct = progressPct(j.done_tasks, j.total_tasks)
              return (
                <li key={j.id} className={ROW}>
                  <div className="min-w-0">
                    <Link
                      to={`/jobs/${j.id}`}
                      className="block truncate text-[12px] text-fg hover:text-accent"
                    >
                      {j.name}
                    </Link>
                  </div>
                  <div className="flex min-w-0 items-center gap-3">
                    <div className="relative h-5 flex-1 overflow-hidden" aria-hidden="true">
                      {/* The now rule, pinned to the right edge of every track. */}
                      <span className="absolute inset-y-0 right-0 w-px bg-accent/50" />
                      <span
                        data-bar-status={j.status}
                        data-instant={g.instant ? 'true' : 'false'}
                        style={{ left: `${g.leftPct}%`, width: `${g.widthPct}%` }}
                        className={`absolute inset-y-0 min-w-[3px] rounded-[4px] border ${c.text} ${c.dot}`}
                      >
                        {j.status === 'running' && (
                          <span data-live-dot="true" className="absolute inset-0" />
                        )}
                      </span>
                    </div>
                    <span className={`flex-none font-mono text-[10px] ${c.text}`}>
                      {j.status}
                      {g.instant ? ' - not started' : ''} - {pct}% -{' '}
                      {formatDuration(j.started_at, j.finished_at)}
                    </span>
                  </div>
                </li>
              )
            })}
          </ul>
        </>
      )}
    </GlassPanel>
  )
}
