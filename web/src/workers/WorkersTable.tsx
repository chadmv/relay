import { StatusDot } from './StatusDot'
import { formatRelativeTime, labelChips, livenessView, specLine } from './liveness'
import type { Worker, WorkerSort } from './api'

export type SortField = 'name' | 'status' | 'last_seen_at'

const COLS = 'grid grid-cols-[1fr_120px_70px_140px_1.2fr_120px]'

function caret(field: SortField, sort: WorkerSort): string {
  if (sort.replace('-', '') !== field) return ''
  return sort.startsWith('-') ? ' ▼' : ' ▲'
}

export function WorkersTable({
  workers,
  sort,
  onSort,
}: {
  workers: Worker[]
  sort: WorkerSort
  onSort: (field: SortField) => void
}) {
  return (
    <div className="rounded-card border border-border bg-white/5 backdrop-blur">
      <div className={`${COLS} border-b border-border px-4 py-3 font-mono text-[10px] tracking-wider text-fg-mute`}>
        <button type="button" className="text-left" onClick={() => onSort('name')}>
          NAME{caret('name', sort)}
        </button>
        <button type="button" className="text-left" onClick={() => onSort('status')}>
          STATUS{caret('status', sort)}
        </button>
        <span>SLOTS</span>
        <span>SPEC</span>
        <span>LABELS</span>
        <button type="button" className="text-left" onClick={() => onSort('last_seen_at')}>
          LAST SEEN{caret('last_seen_at', sort)}
        </button>
      </div>
      {workers.map((w) => (
        <div
          key={w.id}
          className={`${COLS} items-center border-b border-border/40 px-4 py-2 font-mono text-[11.5px] ${livenessView(w.status).dimClass}`}
        >
          <span className="text-fg">{w.name}</span>
          <span><StatusDot status={w.status} /></span>
          <span className="text-fg-mute">{w.max_slots}</span>
          <span className="text-[10.5px] text-fg-mute">{specLine(w)}</span>
          <span className="flex flex-wrap gap-1">
            {labelChips(w.labels).map((c) => (
              <span
                key={c}
                className="rounded-full border border-accent/40 bg-accent/10 px-1.5 py-0.5 text-[9.5px] text-accent"
              >
                {c}
              </span>
            ))}
          </span>
          <span className="text-fg-mute">
            {w.last_seen_at ? formatRelativeTime(w.last_seen_at) : '-'}
          </span>
        </div>
      ))}
    </div>
  )
}
