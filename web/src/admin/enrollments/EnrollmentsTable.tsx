import { Chip, Table, TableCell, TableRow, type TableColumn } from '../../components/holo'
import { deriveStatus, formatExpiryLabel, statusTone } from './enrollmentStatus'
import type { AgentEnrollment, EnrollmentSort, EnrollmentSortField } from './api'

// HOSTNAME HINT | CREATED | EXPIRES | STATUS | NOTE.
//
// Two hi-fi columns are omitted (hifi3-holo-pages.jsx:2164):
//  - TOKEN PREFIX: only tokenhash.Hash(rawHex) is stored, no prefix column exists
//    and nothing returns one.
//  - CREATED BY: created_by is a bare user UUID with no join to `users`, so the
//    cell could only show 36 opaque characters.
// The hi-fi's ACTIONS header is renamed NOTE: the cell holds prose, and a header
// promising actions while delivering a sentence is itself a dead affordance. There
// is no DELETE /v1/agent-enrollments/{id} in v1.
// CREATED is added because it is the default sort key and needs a clickable header.
const COLS = 'grid-cols-[1.6fr_130px_130px_120px_1fr]'

const HEADERS: TableColumn<EnrollmentSortField>[] = [
  { label: 'HOSTNAME HINT' },
  { label: 'CREATED', field: 'created_at' },
  { label: 'EXPIRES', field: 'expires_at' },
  { label: 'STATUS' },
  { label: 'NOTE', align: 'right' },
]

interface EnrollmentsTableProps {
  enrollments: AgentEnrollment[]
  sort: EnrollmentSort
  onSort: (field: EnrollmentSortField) => void
  // Injected so the pill and the relative label are pure functions of props. The
  // tab supplies useNow(60_000); tests supply a fixed Date.
  now: Date
}

export function EnrollmentsTable({ enrollments, sort, onSort, now }: EnrollmentsTableProps) {
  return (
    <div className="rounded-card border border-border bg-gradient-to-b from-white/[0.06] to-white/[0.02] backdrop-blur-[8px]">
      <Table
        label="Agent enrollments"
        columns={COLS}
        headers={HEADERS}
        sort={sort}
        onSort={onSort}
        headerClassName="px-[18px] py-3 tracking-[0.16em]"
      >
        {enrollments.map((e) => {
          const status = deriveStatus(e.expires_at, now)
          return (
            <TableRow
              key={e.id}
              className={`border-b border-accent/[0.06] px-[18px] py-2.5 font-mono text-[11.5px] ${
                status === 'EXPIRED' ? 'opacity-[0.55]' : ''
              }`}
            >
              {/* The key is ABSENT (not null) when unset
                  (internal/api/agent_enrollments.go:90-92), so this is a plain
                  ASCII hyphen placeholder - never an em dash. */}
              <TableCell className="truncate font-sans text-[12.5px] text-fg">
                {e.hostname_hint ?? <span className="text-fg-dim">-</span>}
              </TableCell>
              <TableCell className="text-[10.5px] text-fg-mute">{e.created_at.slice(0, 10)}</TableCell>
              <TableCell className={`text-[11px] ${status === 'ACTIVE' ? 'text-fg' : 'text-fg-mute'}`}>
                {formatExpiryLabel(e.expires_at, now)}
              </TableCell>
              <TableCell>
                <Chip tone={statusTone(status)}>{status}</Chip>
              </TableCell>
              <TableCell className="text-right text-[10.5px] tracking-[0.04em] text-fg-dim">
                consumed on first agent connect
              </TableCell>
            </TableRow>
          )
        })}
      </Table>
    </div>
  )
}
