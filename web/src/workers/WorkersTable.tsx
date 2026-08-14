import { Link } from 'react-router-dom'
import { StatusDot, Table, TableCell, TableRow, type TableColumn } from '../components/holo'
import { formatRelativeTime, labelChips, livenessView, specLine } from './liveness'
import type { Worker, WorkerSort } from './api'

export type SortField = 'name' | 'status' | 'last_seen_at'

const COLS = 'grid-cols-[1fr_120px_70px_140px_1.2fr_120px]'
// Fixed tracks total 450px; 680 gives NAME and LABELS about 100px each.
const MIN_W = 'min-w-[680px]'

const HEADERS: TableColumn<SortField>[] = [
  { label: 'NAME', field: 'name' },
  { label: 'STATUS', field: 'status' },
  { label: 'SLOTS' },
  { label: 'SPEC' },
  { label: 'LABELS' },
  { label: 'LAST SEEN', field: 'last_seen_at' },
]

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
    // The frame stays with the caller and is deliberately left as-is: adopting
    // GlassPanel here would add the gradient and shadow, which is a visible change.
    <div className="rounded-card border border-border bg-white/5 backdrop-blur">
      <Table
        label="Workers"
        columns={COLS}
        minWidth={MIN_W}
        headers={HEADERS}
        sort={sort}
        onSort={onSort}
        headerClassName="px-4 py-3 tracking-wider"
      >
        {workers.map((w) => (
          <TableRow
            key={w.id}
            className={`border-b border-border/40 px-4 py-2 font-mono text-[11.5px] ${livenessView(w.status).dimClass}`}
          >
            <TableCell>
              <Link to={`/workers/${w.id}`} className="text-fg hover:text-accent">
                {w.name}
              </Link>
            </TableCell>
            <TableCell>
              <StatusDot status={w.status} />
            </TableCell>
            <TableCell className="text-fg-mute">{w.max_slots}</TableCell>
            <TableCell className="text-[10.5px] text-fg-mute">{specLine(w)}</TableCell>
            <TableCell className="flex flex-wrap gap-1">
              {labelChips(w.labels).map((c) => (
                <span
                  key={c}
                  className="rounded-full border border-accent/40 bg-accent/10 px-1.5 py-0.5 text-[9.5px] text-accent"
                >
                  {c}
                </span>
              ))}
            </TableCell>
            <TableCell className="text-fg-mute">
              {w.last_seen_at ? formatRelativeTime(w.last_seen_at) : '-'}
            </TableCell>
          </TableRow>
        ))}
      </Table>
    </div>
  )
}
