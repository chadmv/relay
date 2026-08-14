import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { GlassPanel, Table, TableCell, TableRow, type TableColumn } from '../components/holo'
import type { Schedule } from './api'
import { formatRelativeTime, nextRunDisplay, shortId } from './format'

const COLS = 'grid-cols-[1.4fr_120px_110px_90px_1fr_1fr_110px_1.3fr_150px]'
// Nine columns, 580px of fixed track before any fr gets a pixel - the worst case in
// the app. 1040 gives the 4.7fr of flexible tracks about 100px each.
const MIN_W = 'min-w-[1040px]'

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
}: {
  schedules: Schedule[]
  pendingId: string | null
  onRunNow: (id: string) => void
  onToggleEnabled: (id: string, nextEnabled: boolean) => void
  footer?: ReactNode
}) {
  if (schedules.length === 0) {
    return (
      <div className="flex flex-col gap-4">
        <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center text-[13px] text-fg-mute">
          No schedules yet.
        </GlassPanel>
        {footer && <div className="px-1">{footer}</div>}
      </div>
    )
  }
  return (
    <GlassPanel data-testid="schedules-table">
      <Table label="Schedules" columns={COLS} minWidth={MIN_W} headers={HEADERS} headerClassName="px-4 py-3 tracking-wider">
        {schedules.map((s) => {
          const pending = pendingId === s.id
          return (
            <TableRow
              key={s.id}
              className={`border-b border-border/40 px-4 py-2 font-mono text-[11.5px] ${s.enabled ? '' : 'opacity-[0.55]'}`}
            >
              <TableCell className="flex min-w-0 items-center gap-2">
                <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${s.enabled ? 'bg-ok' : 'bg-fg-dim'}`} />
                <Link
                  to={`/schedules/${s.id}`}
                  className="truncate font-sans text-[13px] text-fg hover:text-accent"
                >
                  {s.name}
                </Link>
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
              <TableCell className="text-[10.5px] text-fg-mute">{shortId(s.last_job_id)}</TableCell>
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
