import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import {
  GlassPanel,
  Table,
  TableCell,
  TableRow,
  TOP_LEVEL_HEADER_CLASS,
  TOP_LEVEL_ROW_PX,
  type TableColumn,
} from '../components/holo'
import type { Schedule } from './api'
import { formatRelativeTime, nextRunDisplay, shortId } from './format'
import { statusColor } from '../jobs/status'

const COLS = 'grid-cols-[1.4fr_120px_110px_90px_1fr_1fr_150px_1.3fr_150px]'
// Nine columns, 620px of fixed track before any fr gets a pixel - the worst case in
// the app. 1080 gives the 4.7fr of flexible tracks about 100px each.
//
// The LAST JOB track carries a dot, eight monospace characters and a status word,
// which does not fit in the 110px it held while the cell was an id alone. THE E2E
// LAYOUT GATE CANNOT SEE THIS CHANGE: Table wraps the whole grid in a horizontal
// scroller, so anything that widens the grid scrolls inside that wrapper instead of
// widening the document. The screenshots are the artifact.
const MIN_W = 'min-w-[1080px]'

const HEADERS: TableColumn[] = [
  { label: 'NAME' },
  { label: 'CRON' },
  { label: 'TZ' },
  { label: 'OVERLAP' },
  { label: 'NEXT RUN' },
  { label: 'LAST RUN' },
  { label: 'LAST JOB' },
  { label: 'OWNER' },
  { label: 'ACTIONS', align: 'right' },
]

export function SchedulesTable({
  schedules,
  pendingId,
  onRunNow,
  onToggleEnabled,
  footer,
  // Defaulted to the unfiltered sentence so a caller with no filters keeps saying
  // the true thing without opting in. A caller that IS filtering must pass the
  // filtered sentence, because "No schedules yet." is false the moment a filter
  // is narrowing the set.
  emptyMessage = 'No schedules yet.',
}: {
  schedules: Schedule[]
  pendingId: string | null
  onRunNow: (id: string) => void
  onToggleEnabled: (id: string, nextEnabled: boolean) => void
  footer?: ReactNode
  emptyMessage?: string
}) {
  if (schedules.length === 0) {
    return (
      <div className="flex flex-col gap-4">
        <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center text-[13px] text-fg-mute">
          {emptyMessage}
        </GlassPanel>
        {footer && <div className="px-1">{footer}</div>}
      </div>
    )
  }
  return (
    <GlassPanel data-testid="schedules-table">
      <Table label="Schedules" columns={COLS} minWidth={MIN_W} headers={HEADERS} headerClassName={TOP_LEVEL_HEADER_CLASS}>
        {schedules.map((s) => {
          const pending = pendingId === s.id
          return (
            <TableRow
              key={s.id}
              className={`border-b border-border/40 ${TOP_LEVEL_ROW_PX} py-2 font-mono text-[11.5px] ${s.enabled ? '' : 'opacity-[0.55]'}`}
            >
              <TableCell className="flex min-w-0 items-center gap-2">
                <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${s.enabled ? 'bg-ok' : 'bg-fg-dim'}`} />
                <Link
                  to={`/schedules/${s.id}`}
                  className="truncate font-sans text-[13px] text-fg hover:text-accent"
                >
                  {s.name}
                </Link>
                {/* THE FAILURE MARKER LIVES INSIDE THE NAME CELL RATHER THAN IN A
                    TENTH COLUMN. COLS above is already nine tracks with 620px of
                    fixed width, the worst case in the app; a tenth would push
                    MIN_W up again and this table is already the app's widest.
                    This cell is already a flex row with a gap, so the chip costs
                    no grid change at all.

                    TEXT, NOT A COLOUR. A bare colour is not accessible, and the
                    dot's two states are already spoken for by `enabled`. A
                    failing schedule IS still enabled - relay does not
                    auto-disable one - so this is a second, independent axis with
                    its own element.

                    A TRUTHINESS TEST, NOT `!== undefined`. The server omits
                    last_error entirely for a healthy schedule (omitempty) and
                    never stores an empty string, but the three cases - absent,
                    "" and present - must stay distinguishable at the read, and
                    an empty string carries no reason an operator could act on.
                    Marking a row FAILING with nothing to show would re-create
                    this slice's own defect one layer up.

                    The marker does not shrink: the Link beside it truncates, so
                    under a narrow viewport the NAME should lose characters
                    before the marker disappears. Measured in a real browser by
                    web/e2e/layout.spec.ts's `schedules-failing` surface; jsdom
                    performs no layout and can say nothing about it. */}
                {s.last_error ? (
                  <span
                    title="The scheduler could not produce a job from this schedule. Open it for the reason."
                    className="shrink-0 rounded-full border border-err/40 bg-err/10 px-1.5 py-0.5 text-[9.5px] tracking-wider text-err"
                  >
                    FAILING
                  </span>
                ) : null}
              </TableCell>
              <TableCell className="text-fg">{s.cron_expr}</TableCell>
              <TableCell className="truncate text-[10.5px] text-fg-mute">{s.timezone}</TableCell>
              <TableCell>
                <span
                  className={`rounded-full border border-border px-1.5 py-0.5 text-[9.5px] uppercase tracking-wider ${s.overlap_policy === 'allow' ? 'text-accent' : 'text-fg-mute'}`}
                >
                  {s.overlap_policy}
                </span>
              </TableCell>
              <TableCell className={s.enabled ? 'text-fg' : 'text-fg-dim'}>
                {s.enabled ? <span className="text-accent-b">&#9658;</span> : null} {nextRunDisplay(s.next_run_at)}
              </TableCell>
              <TableCell className="text-fg-mute">{s.last_run_at ? formatRelativeTime(s.last_run_at) : '-'}</TableCell>
              {/* THE STATUS IS A WORD, not only the dot's colour. The row already
                  carries a dot four columns to the left meaning `enabled`, so a
                  second dot with a different vocabulary and no label is ambiguous
                  to a sighted reader as well as inaccessible - the same judgement
                  the FAILING chip above records.

                  statusColor is reused from the jobs status helper exactly as
                  ScheduleRunsPanel reuses it in this same feature area; there is
                  no second mapping here. A status outside the union takes that
                  helper's default branch and renders muted, with the verbatim word
                  still carrying the fact.

                  NO DOT AND NO WORD WHEN last_job_status IS ABSENT. The server
                  emits the two keys together or not at all, so this state cannot
                  occur - and drawing a neutral dot from an absent key would forge
                  a fact, which is the defect fillLastJobStatuses refuses to commit
                  by failing the request. The bare link is a fail-quiet.

                  RUN NOW DOES NOT ADVANCE THIS. POST /run-now creates a job but
                  updates neither last_job_id nor last_run_at, so immediately after
                  an operator clicks Run now this cell still describes the previous
                  SCHEDULED fire. See the README's Scheduled Jobs section. */}
              <TableCell className="text-[10.5px] text-fg-mute">
                {s.last_job_id ? (
                  <Link
                    to={`/jobs/${s.last_job_id}`}
                    className="inline-flex items-center gap-1.5 hover:text-accent"
                  >
                    {s.last_job_status ? (
                      <span
                        aria-hidden="true"
                        data-status={s.last_job_status}
                        className={`h-1.5 w-1.5 shrink-0 rounded-full ${statusColor(s.last_job_status).dot}`}
                      />
                    ) : null}
                    <span>{shortId(s.last_job_id)}</span>
                    {s.last_job_status ? <>{' '}<span>{s.last_job_status}</span></> : null}
                  </Link>
                ) : (
                  '-'
                )}
              </TableCell>
              <TableCell className="truncate text-[10.5px] text-fg-mute">{s.owner_email}</TableCell>
              <TableCell className="flex justify-end gap-1.5">
                <button
                  type="button"
                  disabled={pending}
                  onClick={() => onRunNow(s.id)}
                  className="rounded-md border border-accent/50 bg-accent/15 px-2.5 py-1 text-[11px] text-fg disabled:opacity-40"
                >
                  Run now
                </button>
                <button
                  type="button"
                  disabled={pending}
                  onClick={() => onToggleEnabled(s.id, !s.enabled)}
                  className="rounded-md border border-border bg-white/5 px-2.5 py-1 text-[11px] text-fg-mute disabled:opacity-40"
                >
                  {s.enabled ? 'Disable' : 'Enable'}
                </button>
                {/* A react-router <Link>, not a useNavigate handler on a button, so
                    middle-click and open-in-new-tab work and no callback has to be
                    threaded through this component's props. Row identity in the
                    accessible name, matching UsersTable.tsx:169-199. */}
                <Link
                  to={`/schedules/${s.id}`}
                  aria-label={`Edit ${s.name}`}
                  className="rounded-md border border-border bg-white/5 px-2.5 py-1 text-[11px] text-fg-mute"
                >
                  Edit
                </Link>
              </TableCell>
            </TableRow>
          )
        })}
      </Table>
      {/* Outside the table subtree: a footer is not a valid child of role="table". */}
      {footer && <div className="border-t border-border px-4 py-3">{footer}</div>}
    </GlassPanel>
  )
}
