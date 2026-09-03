import {
  Chip,
  NESTED_HEADER_CLASS,
  NESTED_ROW_PX,
  Table,
  TableCell,
  TableRow,
  type TableColumn,
} from '../components/holo'
import { deriveStatus, statusTone } from '../admin/reservations/reservationStatus'
import { formatDateTime } from '../lib/time'
import { useWorkerReservations } from './useWorkerReservations'

const COLS = 'grid-cols-[1.3fr_1fr_90px_110px]'
// Fixed tracks sum to 200px, under this 460px min-width, and both flexible cells
// carry truncate - the precondition Table.tsx states for the min-width budget to
// hold. This is ARITHMETIC, not a measurement: no layout engine has rendered this
// panel at any width. See docs/backlog/idea-2026-09-02-measure-the-populated-worker-detail-panels.md.
const MIN_W = 'min-w-[460px]'

// ONE literal for the panel title and the table's accessible name. The structural
// test on WorkerDetailPage pins the RENDERED pair, since a test comparing two
// references to this constant could not fail.
export const WORKER_RESERVATIONS_PANEL_TITLE = 'Reservations'

const HEADERS: TableColumn[] = [
  { label: 'NAME' },
  { label: 'PROJECT' },
  { label: 'STATUS' },
  { label: 'ENDS', align: 'right' },
]

// Reservations naming this worker in worker_ids. Rendered inside the page's Panel
// (which supplies the frame and the title), so this component is the header row,
// the data rows and the panel-level states.
//
// No SELECTOR column and no WORKERS column: every row here names this worker by
// construction, and the selector enforces nothing. No Delete: deleting from a
// worker's page would act on every other worker the reservation names, invisibly.
//
// `now` is a prop so the status pill is a pure function of props and a test can
// supply a fixed Date.
export function WorkerReservationsPanel({ workerId, now }: { workerId: string; now: Date }) {
  const { data, isLoading, error, refetch } = useWorkerReservations(workerId)
  const rows = data?.items ?? []
  const anyActive = rows.some((r) => deriveStatus(r, now) === 'ACTIVE')

  return (
    <div className="flex flex-col">
      <Table
        label={WORKER_RESERVATIONS_PANEL_TITLE}
        columns={COLS}
        minWidth={MIN_W}
        headers={HEADERS}
        headerClassName={NESTED_HEADER_CLASS}
      >
        {rows.map((r) => {
          const status = deriveStatus(r, now)
          return (
            <TableRow
              key={r.id}
              className={`border-b border-border/40 ${NESTED_ROW_PX} py-2 font-mono text-[11px]`}
            >
              <TableCell className="truncate text-fg">{r.name}</TableCell>
              <TableCell className="truncate text-fg-mute">{r.project ?? '-'}</TableCell>
              <TableCell>
                <Chip tone={statusTone(status)}>{status}</Chip>
              </TableCell>
              {/* An absent ends_at is the FACT, not a missing value: an open-ended
                  reservation excludes this worker from dispatch indefinitely. The
                  admin table's hyphen is right THERE because STARTS and ENDS are
                  read as a pair; here it would read as an unknown value. */}
              <TableCell className="text-right text-[10.5px] text-fg-mute">
                {r.ends_at ? formatDateTime(r.ends_at) : 'no end'}
              </TableCell>
            </TableRow>
          )
        })}
      </Table>

      {/* The loading line, the error banner, the short-page footer and the empty
          state are SIBLINGS of the role="table" subtree, never children: none is a
          valid child of a table, and the header row must stay present in every
          state. */}
      {isLoading && !data && (
        <div className="px-4 py-3 font-mono text-[11px] tracking-[0.04em] text-fg-dim">
          loading reservations...
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

      {data && (data.next_cursor !== '' || rows.length < data.total) && (
        <div className="px-4 py-2 font-mono text-[10px] tracking-[0.04em] text-fg-dim">
          {`showing ${rows.length} of ${data.total}`}
        </div>
      )}

      {!isLoading && !error && rows.length === 0 && (
        <div className="px-4 py-3 text-[12px] text-fg-mute">No reservation targets this worker.</div>
      )}

      {/* Absent when no row is active, rather than issuing an all-clear this panel
          cannot back: status, the disabled flag, labels and free slots are all
          separate dispatch gates and none of them is visible here. */}
      {anyActive && (
        <div className="px-4 pb-2 font-mono text-[10px] tracking-[0.04em] text-fg-dim">
          the scheduler skips this worker while a reservation is active.
        </div>
      )}

      {/* The panel's correctness statement, not a caption. The filter matches
          worker_ids containment alone, so a selector-only reservation is absent
          from this list; without this line that absence reads as a missing row. */}
      <div className="px-4 pb-3 font-mono text-[10px] tracking-[0.04em] text-fg-dim">
        {'selectors are informational in v1 \u00b7 only worker_ids are enforced.'}
      </div>
    </div>
  )
}
