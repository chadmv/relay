import { GlassPanel, Table, TableCell, TableRow, type TableColumn } from '../components/holo'
import type { TaskDetail } from './api'
import { taskStatusColor } from './taskStatus'

const COLS = 'grid-cols-[1fr_110px_80px_120px_1fr]'

const HEADERS: TableColumn[] = [
  { label: 'NAME' },
  { label: 'STATUS' },
  { label: 'RETRY' },
  { label: 'WORKER' },
  { label: 'DEPS' },
]

// Tasks table. Rows are SELECTION controls, not navigation: clicking a row sets
// the selected task that drives the Spec/Log panes. Uses aria-selected on each
// row (role=row inside role=table). No per-task duration/percent column: the API
// returns neither per-task timing nor a percent
// (docs/backlog/feature-2026-07-01-per-task-timing.md). The worker cell stays
// plain text; a link to the worker is a deferred follow-up.
export function TasksTable({
  tasks,
  selectedTaskId,
  onSelect,
}: {
  tasks: TaskDetail[]
  selectedTaskId: string
  onSelect: (id: string) => void
}) {
  if (tasks.length === 0) {
    return <GlassPanel className="p-4 text-[12px] text-fg-mute">No tasks.</GlassPanel>
  }
  return (
    <GlassPanel>
      <Table label="Tasks" columns={COLS} headers={HEADERS} headerClassName="px-4 py-2 tracking-wider">
        {tasks.map((t) => {
          const c = taskStatusColor(t.status)
          const selected = t.id === selectedTaskId
          return (
            <TableRow
              key={t.id}
              as="button"
              type="button"
              aria-selected={selected}
              onClick={() => onSelect(t.id)}
              className={`w-full border-b border-border/40 px-4 py-2 text-left font-mono text-[11.5px] ${
                selected ? 'border-l-2 border-accent bg-accent/[0.08]' : ''
              }`}
            >
              <TableCell className="truncate font-sans text-[13px] text-fg">{t.name}</TableCell>
              <TableCell className={`flex items-center gap-2 ${c.text}`}>
                <span className={`h-1.5 w-1.5 rounded-full ${c.dot}`} />
                {t.status}
              </TableCell>
              <TableCell className="text-fg-mute">
                {t.retry_count}/{t.retries}
              </TableCell>
              <TableCell className="truncate text-fg-mute">{t.worker_id ? t.worker_id.slice(0, 6) : '-'}</TableCell>
              <TableCell className="truncate text-fg-mute">
                {t.depends_on && t.depends_on.length > 0 ? t.depends_on.join(', ') : '-'}
              </TableCell>
            </TableRow>
          )
        })}
      </Table>
    </GlassPanel>
  )
}
