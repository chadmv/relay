import { Link } from 'react-router-dom'
import { Table, TableCell, TableRow, type TableColumn } from '../components/holo'
import { taskStatusColor } from '../jobs/taskStatus'
import { formatRelativeTime } from './liveness'
import { useWorkerTasks } from './useWorkerTasks'

const COLS = 'grid-cols-[1fr_1fr_100px_90px_60px]'
// Fixed tracks sum to 250px, under this 560px min-width. Both flexible cells
// carry truncate, which is the precondition Table.tsx states for the min-width
// budget to hold - a flexible cell without it would need its own content
// minimum added to the sum.
const MIN_W = 'min-w-[560px]'

const HEADERS: TableColumn[] = [
  { label: 'TASK' },
  { label: 'JOB' },
  { label: 'STATUS' },
  { label: 'STARTED' },
  { label: 'RETRY', align: 'right' },
]

// The worker's currently assigned tasks. Rendered inside the page's Panel (which
// supplies the frame and the "Current tasks" title), so this component is only
// the header row, the data rows and the panel-level states.
//
// No progress column is rendered: relay has none on the row, in the proto or in
// the agent, so there is nothing to render honestly.
export function WorkerTasksPanel({ workerId }: { workerId: string }) {
  const { data, isLoading, error, refetch } = useWorkerTasks(workerId)
  const rows = data?.items ?? []

  return (
    <div className="flex flex-col">
      {/* aria-label matches the visible title on the page Panel that wraps this. */}
      <Table
        label="Current tasks"
        columns={COLS}
        minWidth={MIN_W}
        headers={HEADERS}
        headerClassName="px-4 py-2 tracking-wider"
      >
        {rows.map((t) => {
          const c = taskStatusColor(t.status)
          return (
            <TableRow
              key={t.id}
              className="border-b border-border/40 px-4 py-2 font-mono text-[11px]"
            >
              <TableCell className="truncate">
                <Link
                  to={`/jobs/${t.job_id}/tasks/${t.id}`}
                  className="text-accent hover:text-accent-b"
                >
                  {t.name}
                </Link>
              </TableCell>
              <TableCell className="truncate">
                <Link to={`/jobs/${t.job_id}`} className="text-fg-mute hover:text-fg">
                  {t.job_name}
                </Link>
              </TableCell>
              <TableCell className={`flex items-center gap-2 ${c.text}`}>
                <span className={`h-1.5 w-1.5 rounded-full ${c.dot}`} />
                {t.status}
              </TableCell>
              {/* A dispatched task spends the whole workspace sync with no
                  started_at, and that is the row this panel most exists to show. */}
              <TableCell className="text-fg-mute">
                {t.started_at ? formatRelativeTime(t.started_at) : 'not started'}
              </TableCell>
              <TableCell className="text-right text-fg-mute">
                {t.retries > 0 ? `${t.retry_count}/${t.retries}` : '-'}
              </TableCell>
            </TableRow>
          )
        })}
      </Table>

      {/* The loading line, the error banner and the empty state are SIBLINGS of
          the role="table" subtree, never children: none is a valid child of a
          table, and the header row must stay present in every state. */}
      {isLoading && !data && (
        <div className="px-4 py-3 font-mono text-[11px] tracking-[0.04em] text-fg-dim">
          loading tasks...
        </div>
      )}

      {error ? (
        <div className="mx-4 my-2 flex items-center justify-between gap-3 rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err">
          <span>{(error as Error).message}</span>
          <button type="button" className="text-[11px] underline" onClick={() => refetch()}>
            Retry
          </button>
        </div>
      ) : null}

      {/* One message, not the hi-fi's two. An offline worker inside the grace
          window still has its tasks assigned, so an empty list does not
          establish that being offline is the reason. */}
      {!isLoading && !error && rows.length === 0 && (
        <div className="px-4 py-3 text-[12px] text-fg-mute">No active tasks.</div>
      )}
    </div>
  )
}
