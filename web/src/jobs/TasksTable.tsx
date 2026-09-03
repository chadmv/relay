import {
  GlassPanel,
  NESTED_HEADER_CLASS,
  NESTED_ROW_PX,
  Table,
  TableCell,
  TableRow,
  type TableColumn,
} from '../components/holo'
import type { TaskDetail } from './api'
import { taskStatusColor } from './taskStatus'

const COLS = 'grid-cols-[1fr_110px_80px_120px_1fr]'
// Fixed tracks total 310px; lives in JobDetailPage's lg:w-[55%] column (~682px at
// 1280). The row keeps its own onClick, so the whole scrolled width stays a mouse
// target even though only the name cell is keyboard-focusable.
const MIN_W = 'min-w-[560px]'

const HEADERS: TableColumn[] = [
  { label: 'NAME' },
  { label: 'STATUS' },
  { label: 'RETRY' },
  { label: 'WORKER' },
  { label: 'DEPS' },
]

// Tasks table. Rows are SELECTION controls, not navigation: activating a row's
// name-cell button sets the selected task that drives the Spec/Log panes.
//
// ONE HANDLER, BY BUBBLING. The row carries onClick; the name-cell button carries
// NONE of its own. A mouse click, an Enter or a Space on the button dispatches a
// click that bubbles to the row, so every input mode produces exactly one onSelect
// call and the whole row stays a mouse target. Adding an onClick to the button, or
// a stopPropagation anywhere in the cell, breaks one input mode and not the other:
// `each task row exposes a button named for the task, and one activation selects
// once` pins the call count, and the job-detail describe in web/e2e/keyboard.spec.ts
// pins the key-press half in a real browser.
//
// NO aria-selected AND NO interactive row element: this table implements
// neither grid nor listbox semantics, so it advertises none. aria-selected is
// not surfaced under role="table", and an interactive row element would
// replace the row role. The selected task is marked with aria-current on its
// button instead, which is valid on any element.
//
// No per-task duration/percent column: the API returns neither per-task timing nor
// a percent (docs/backlog/feature-2026-07-01-per-task-timing.md). The worker cell
// stays plain text; a link to the worker is a deferred follow-up.
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
      <Table label="Tasks" columns={COLS} minWidth={MIN_W} headers={HEADERS} headerClassName={NESTED_HEADER_CLASS}>
        {tasks.map((t) => {
          const c = taskStatusColor(t.status)
          const selected = t.id === selectedTaskId
          return (
            <TableRow
              key={t.id}
              onClick={() => onSelect(t.id)}
              className={`w-full border-b border-border/40 ${NESTED_ROW_PX} py-2 text-left font-mono text-[11.5px] ${
                selected ? 'border-l-2 border-accent bg-accent/[0.08]' : ''
              }`}
            >
              <TableCell className="truncate font-sans text-[13px] text-fg">
                {/* The button fills this cell exactly (w-full) and both carry
                    `truncate` (overflow: hidden), so a ring drawn outside the
                    border box is clipped to zero visible pixels by the cell's
                    own clip. A negative outline offset draws the ring
                    INSIDE the box instead, which that clip cannot reach. Pinned
                    by `the name-cell button carries a negative-offset focus
                    ring` (unit) and the job-detail keyboard describe (browser,
                    real focus + getComputedStyle). */}
                <button
                  type="button"
                  aria-current={selected ? 'true' : undefined}
                  className="block w-full truncate text-left focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-accent"
                >
                  {t.name}
                </button>
              </TableCell>
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
