import { Link } from 'react-router-dom'
import { StatusDot } from '../components/holo/StatusDot'
import { formatRelativeTime, labelChips, livenessView, specLine } from './liveness'
import type { Worker, WorkerSort } from './api'

export type SortField = 'name' | 'status' | 'last_seen_at'

const COLS = 'grid grid-cols-[1fr_120px_70px_140px_1.2fr_120px]'

function caret(field: SortField, sort: WorkerSort): string {
  if (sort.replace('-', '') !== field) return ''
  return sort.startsWith('-') ? ' ▼' : ' ▲'
}

function ariaSort(field: SortField, sort: WorkerSort): 'ascending' | 'descending' | 'none' {
  if (sort.replace('-', '') !== field) return 'none'
  return sort.startsWith('-') ? 'descending' : 'ascending'
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
    <div role="table" aria-label="Workers" className="rounded-card border border-border bg-white/5 backdrop-blur">
      <div role="row" className={`${COLS} border-b border-border px-4 py-3 font-mono text-[10px] tracking-wider text-fg-mute`}>
        <div role="columnheader" aria-sort={ariaSort('name', sort)}>
          <button type="button" className="text-left" onClick={() => onSort('name')}>
            NAME{caret('name', sort)}
          </button>
        </div>
        <div role="columnheader" aria-sort={ariaSort('status', sort)}>
          <button type="button" className="text-left" onClick={() => onSort('status')}>
            STATUS{caret('status', sort)}
          </button>
        </div>
        <span role="columnheader">SLOTS</span>
        <span role="columnheader">SPEC</span>
        <span role="columnheader">LABELS</span>
        <div role="columnheader" aria-sort={ariaSort('last_seen_at', sort)}>
          <button type="button" className="text-left" onClick={() => onSort('last_seen_at')}>
            LAST SEEN{caret('last_seen_at', sort)}
          </button>
        </div>
      </div>
      {workers.map((w) => (
        <div
          key={w.id}
          role="row"
          className={`${COLS} items-center border-b border-border/40 px-4 py-2 font-mono text-[11.5px] ${livenessView(w.status).dimClass}`}
        >
          <span role="cell">
            <Link to={`/workers/${w.id}`} className="text-fg hover:text-accent">
              {w.name}
            </Link>
          </span>
          <span role="cell"><StatusDot status={w.status} /></span>
          <span role="cell" className="text-fg-mute">{w.max_slots}</span>
          <span role="cell" className="text-[10.5px] text-fg-mute">{specLine(w)}</span>
          <span role="cell" className="flex flex-wrap gap-1">
            {labelChips(w.labels).map((c) => (
              <span
                key={c}
                className="rounded-full border border-accent/40 bg-accent/10 px-1.5 py-0.5 text-[9.5px] text-accent"
              >
                {c}
              </span>
            ))}
          </span>
          <span role="cell" className="text-fg-mute">
            {w.last_seen_at ? formatRelativeTime(w.last_seen_at) : '-'}
          </span>
        </div>
      ))}
    </div>
  )
}
