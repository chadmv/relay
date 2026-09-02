import { Link } from 'react-router-dom'
import { Chip, GlassPanel, Table, TableCell, TableRow, type TableColumn } from '../../components/holo'
import { formatDateTime } from '../../lib/time'
import { deriveStatus, statusTone } from './reservationStatus'
import type { Reservation, ReservationSort, ReservationSortField } from './api'

// NAME | PROJECT | WORKERS | STARTS | ENDS | STATUS | CREATED | ACT.
//
// Against the hi-fi (hifi3-holo-pages.jsx:2205-2278):
//  - The dedicated SELECTOR column is dropped to pay for STATUS and CREATED. A
//    selector, when present, is a `sel` chip beside the name. Every row THIS UI can
//    create has no selector, so a column for it would be permanently empty.
//  - CREATED is added because it is the default sort key and needs a clickable header.
//  - No owner column: user_id is a bare UUID with no join to `users`
//    (internal/api/reservations.go:18, :47).
// The header is WORKERS, not "RESERVED FOR": the listed workers are EXCLUDED from
// dispatch for everyone, so any possessive header would be a claim the scheduler does
// not implement (internal/scheduler/dispatch.go:185-223).
const COLS = 'grid-cols-[1.3fr_110px_1.5fr_130px_130px_110px_110px_100px]'
// Eight columns, 690px of fixed track - second only to SchedulesTable.
const MIN_W = 'min-w-[980px]'

const HEADERS: TableColumn<ReservationSortField>[] = [
  { label: 'NAME', field: 'name' },
  { label: 'PROJECT' },
  { label: 'WORKERS' },
  { label: 'STARTS', field: 'starts_at' },
  { label: 'ENDS', field: 'ends_at' },
  { label: 'STATUS' },
  { label: 'CREATED', field: 'created_at' },
  { label: 'ACT.', align: 'right' },
]

const MINI = 'rounded-full border px-2.5 py-1 font-mono text-[10.5px] tracking-[0.04em] disabled:opacity-40'
const MINI_DANGER = `${MINI} border-err/40 bg-err/10 text-err`

// Absent KEY (not null) for project/starts_at/ends_at: plain ASCII hyphen, never an
// em dash.
const DASH = <span className="text-fg-dim">-</span>

interface ReservationsTableProps {
  reservations: Reservation[]
  sort: ReservationSort
  onSort: (field: ReservationSortField) => void
  // Injected so the status pill is a pure function of props. The tab supplies
  // useNow(60_000); tests supply a fixed Date.
  now: Date
  busy: boolean
  onDelete: (reservation: Reservation) => void
}

export function ReservationsTable({
  reservations,
  sort,
  onSort,
  now,
  busy,
  onDelete,
}: ReservationsTableProps) {
  return (
    <GlassPanel>
      <Table
        label="Reservations"
        columns={COLS}
        minWidth={MIN_W}
        headers={HEADERS}
        sort={sort}
        onSort={onSort}
        headerClassName="px-[18px] py-3 tracking-[0.16em]"
      >
        {reservations.map((r) => {
          const status = deriveStatus(r, now)
          // `selector` can be null (a create with no selector marshals a nil map to the
          // literal `null`) or {} (column default) or pairs - all three must render
          // without null/undefined reaching the DOM.
          const pairs = r.selector ? Object.entries(r.selector) : []
          return (
            <TableRow
              key={r.id}
              className={`border-b border-accent/[0.06] px-[18px] py-2.5 font-mono text-[11.5px] ${
                status === 'ENDED' ? 'opacity-[0.55]' : ''
              }`}
            >
              <TableCell className="flex min-w-0 items-center gap-2">
                <span className="truncate font-sans text-[12.5px] text-fg">{r.name}</span>
                {pairs.length > 0 && (
                  <Chip tone="muted">
                    <span title={pairs.map(([k, v]) => `${k}=${v}`).join(' ')}>sel</span>
                  </Chip>
                )}
              </TableCell>

              <TableCell className="truncate font-sans text-[12px] text-fg-mute">{r.project ?? DASH}</TableCell>

              <TableCell className="flex flex-wrap gap-1">
                {r.worker_ids.length === 0 ? (
                  <span className="text-[11px] text-fg-dim">none</span>
                ) : (
                  // No FK on worker_ids, so a link can 404 on a deleted or revoked
                  // worker. That is the existing detail page's error state, and an
                  // unresolvable id is itself useful information. Wrapping in a Link
                  // rather than giving Chip an href keeps the shared primitive untouched.
                  r.worker_ids.map((id) => (
                    <Link key={id} to={`/workers/${id}`} title={id}>
                      <Chip tone="muted">{id.slice(0, 8)}</Chip>
                    </Link>
                  ))
                )}
              </TableCell>

              <TableCell className="text-[10.5px] text-fg-mute">
                {r.starts_at ? formatDateTime(r.starts_at) : DASH}
              </TableCell>
              <TableCell className="text-[10.5px] text-fg-mute">
                {r.ends_at ? formatDateTime(r.ends_at) : DASH}
              </TableCell>

              <TableCell>
                <Chip tone={statusTone(status)}>{status}</Chip>
              </TableCell>

              <TableCell className="text-[10.5px] text-fg-mute">{r.created_at.slice(0, 10)}</TableCell>

              <TableCell className="flex justify-end">
                {/* Row identity in the accessible name: a page of 50 buttons all named
                    "Delete" is indistinguishable to a screen reader and to a test. */}
                <button
                  type="button"
                  className={MINI_DANGER}
                  disabled={busy}
                  aria-label={`Delete reservation ${r.name}`}
                  onClick={() => onDelete(r)}
                >
                  Delete
                </button>
              </TableCell>
            </TableRow>
          )
        })}
      </Table>
    </GlassPanel>
  )
}
