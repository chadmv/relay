import { Link } from 'react-router-dom'
import {
  NESTED_HEADER_CLASS,
  NESTED_ROW_PX,
  Panel,
  Table,
  TableCell,
  TableRow,
  type TableColumn,
} from '../components/holo'
import type { Job } from '../jobs/api'
import { formatDuration, formatStarted, statusColor } from '../jobs/status'

const COLS = 'grid-cols-[130px_70px_110px_100px_1fr]'
// Fixed tracks total 410px. Sits in a detail-page column (~614px at 1280), so 560
// stays under the container and only scrolls once the column is narrower.
const MIN_W = 'min-w-[560px]'

const HEADERS: TableColumn[] = [
  { label: 'STARTED' },
  { label: 'DUR' },
  { label: 'STATUS' },
  { label: 'JOB ID' },
  { label: 'OWNER' },
]

// A fixed latest-N window of the runs this schedule produced, newest first (the
// server's own order: internal/store/query/jobs.sql:77). No sorting affordance and no
// pager: this is a summary on a detail page, not a list page, and the footer says so.
// Every column is a real field on the list-enriched jobResponse
// (internal/api/jobs.go:55-73); nothing here is fabricated.
export function ScheduleRunsPanel({ runs, total }: { runs: Job[]; total: number }) {
  return (
    <Panel
      title="Recent runs"
      meta="GET /v1/jobs?scheduled_job_id="
      footer={<span>{`latest ${runs.length} of ${total}`}</span>}
    >
      {runs.length === 0 ? (
        <div className="px-4 py-6 font-mono text-[11px] tracking-[0.04em] text-fg-dim">
          this schedule has never fired
        </div>
      ) : (
        <Table label="Recent runs" columns={COLS} minWidth={MIN_W} headers={HEADERS} headerClassName={NESTED_HEADER_CLASS}>
          {runs.map((j) => {
            const c = statusColor(j.status)
            return (
              <TableRow
                key={j.id}
                className={`border-b border-border/40 ${NESTED_ROW_PX} py-2 font-mono text-[11px]`}
              >
                {/* started_at / finished_at keys are ABSENT for a run with no started
                    or finished task (internal/api/jobs.go:119-137); both helpers
                    return '-' for undefined (jobs/status.ts:33-34, :48-49). */}
                <TableCell className="text-fg-mute">{formatStarted(j.started_at)}</TableCell>
                <TableCell className="text-fg-mute">{formatDuration(j.started_at, j.finished_at)}</TableCell>
                <TableCell className={`flex items-center gap-2 ${c.text}`}>
                  <span className={`h-1.5 w-1.5 rounded-full ${c.dot}`} />
                  {j.status}
                </TableCell>
                <TableCell>
                  <Link to={`/jobs/${j.id}`} className="text-accent hover:text-accent-b">
                    {j.id.slice(0, 8)}
                  </Link>
                </TableCell>
                <TableCell className="truncate text-[10.5px] text-fg-mute">
                  {j.submitted_by_email ?? '-'}
                </TableCell>
              </TableRow>
            )
          })}
        </Table>
      )}
    </Panel>
  )
}
