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
import type { Job } from './api'
import { statusColor, progressPct, formatDuration, formatStarted } from './status'

const COLS = 'grid-cols-[90px_1fr_120px_150px_120px_70px_150px]'
// Fixed tracks total 700px; 880 leaves the 1fr NAME column 180px before the table
// scrolls inside its panel. See Table.tsx's narrow-viewport convention.
const MIN_W = 'min-w-[880px]'

const HEADERS: TableColumn[] = [
  { label: 'ID' },
  { label: 'NAME' },
  { label: 'STATUS' },
  { label: 'PROGRESS' },
  { label: 'STARTED' },
  { label: 'DUR' },
  { label: 'OWNER' },
]

export function JobsTable({ jobs, footer }: { jobs: Job[]; footer?: ReactNode }) {
  if (jobs.length === 0) {
    return (
      <div className="flex flex-col gap-4">
        <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center text-[13px] text-fg-mute">
          No jobs yet.
        </GlassPanel>
        {footer && <div className="px-1">{footer}</div>}
      </div>
    )
  }
  return (
    <GlassPanel data-testid="jobs-table">
      <Table
        label="Jobs"
        columns={COLS}
        minWidth={MIN_W}
        headers={HEADERS}
        /* The hi-fi's top-level list header treatment. */
        headerClassName={TOP_LEVEL_HEADER_CLASS}
      >
        {jobs.map((j) => {
          const c = statusColor(j.status)
          const pct = progressPct(j.done_tasks, j.total_tasks)
          return (
            <TableRow
              key={j.id}
              data-testid={`job-row-${j.id}`}
              /* Horizontal padding tracks the header's, and must: the header row and
                 the body rows are sibling grid containers sharing one template, so a
                 disagreement puts every column label off its own data. The vertical
                 component is deliberately unchanged. */
              className={`border-b border-border/40 ${TOP_LEVEL_ROW_PX} py-2 font-mono text-[11.5px] ${
                j.status === 'running' ? 'bg-accent/[0.04]' : ''
              }`}
            >
              <TableCell className="text-fg-mute">{j.id.slice(0, 6)}</TableCell>
              <TableCell className="flex min-w-0 items-center gap-2">
                <Link to={`/jobs/${j.id}`} className="truncate font-sans text-[13px] text-fg hover:text-accent">
                  {j.name}
                </Link>
                {j.scheduled_job_name && (
                  <span className="flex-none rounded-full border border-accent-b/40 bg-accent-b/10 px-1.5 py-0.5 text-[9.5px] text-accent-b">
                    ⟳ {j.scheduled_job_name}
                  </span>
                )}
              </TableCell>
              <TableCell className={`flex items-center gap-2 ${c.text}`}>
                <span className={`h-1.5 w-1.5 rounded-full ${c.dot}`} />
                {j.status}
              </TableCell>
              <TableCell className="grid grid-cols-[1fr_36px] items-center gap-2 pr-4">
                <span className="relative h-1 overflow-hidden rounded bg-white/10">
                  <span
                    className={`absolute inset-y-0 left-0 rounded ${
                      j.status === 'done' ? 'bg-ok' : j.status === 'failed' ? 'bg-err' : 'bg-accent'
                    }`}
                    style={{ width: `${pct}%` }}
                  />
                </span>
                <span className="text-right text-fg">{pct}%</span>
              </TableCell>
              <TableCell className="text-fg-mute">{formatStarted(j.started_at)}</TableCell>
              <TableCell className="text-fg-mute">{formatDuration(j.started_at, j.finished_at)}</TableCell>
              <TableCell className="truncate text-[11px] text-fg-mute">{j.submitted_by_email ?? '-'}</TableCell>
            </TableRow>
          )
        })}
      </Table>
      {/* Outside the table subtree: a footer is not a valid child of role="table".
          It stays inside the GlassPanel, so the surface still contains it. */}
      {footer && <div className="border-t border-border px-4 py-3">{footer}</div>}
    </GlassPanel>
  )
}
