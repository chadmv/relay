import type { TaskDetail } from './api'
import { taskStatusColor } from './taskStatus'

const COLS = 'grid grid-cols-[1fr_110px_80px_120px_1fr]'

// Tasks table. Rows are SELECTION controls, not navigation: clicking a row sets
// the selected task that drives the Spec/Log panes. Uses aria-selected on each
// row (role=row inside role=table). No per-task duration/percent column: the API
// returns neither per-task timing nor a percent.
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
    return (
      <div className="rounded-card border border-border bg-white/5 p-4 text-[12px] text-fg-mute">
        No tasks.
      </div>
    )
  }
  return (
    <div role="table" aria-label="Tasks" className="rounded-card border border-border bg-white/5">
      <div
        role="row"
        className={`${COLS} border-b border-border px-4 py-2 font-mono text-[10px] tracking-wider text-fg-mute`}
      >
        <span role="columnheader">NAME</span>
        <span role="columnheader">STATUS</span>
        <span role="columnheader">RETRY</span>
        <span role="columnheader">WORKER</span>
        <span role="columnheader">DEPS</span>
      </div>
      {tasks.map((t) => {
        const c = taskStatusColor(t.status)
        const selected = t.id === selectedTaskId
        return (
          <button
            key={t.id}
            type="button"
            role="row"
            aria-selected={selected}
            onClick={() => onSelect(t.id)}
            className={`${COLS} w-full items-center border-b border-border/40 px-4 py-2 text-left font-mono text-[11.5px] ${
              selected ? 'bg-accent/10' : ''
            }`}
          >
            <span role="cell" className="truncate font-sans text-[13px] text-fg">{t.name}</span>
            <span role="cell" className={`flex items-center gap-2 ${c.text}`}>
              <span className={`h-1.5 w-1.5 rounded-full ${c.dot}`} />
              {t.status}
            </span>
            <span role="cell" className="text-fg-mute">{t.retry_count}/{t.retries}</span>
            <span role="cell" className="truncate text-fg-mute">
              {t.worker_id ? t.worker_id.slice(0, 6) : '-'}
            </span>
            <span role="cell" className="truncate text-fg-mute">
              {t.depends_on && t.depends_on.length > 0 ? t.depends_on.join(', ') : '-'}
            </span>
          </button>
        )
      })}
    </div>
  )
}
