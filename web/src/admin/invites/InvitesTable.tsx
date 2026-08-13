import { Chip, Table, TableCell, TableRow, type TableColumn } from '../../components/holo'
import { deriveStatus, formatExpiryLabel, statusTone } from './inviteStatus'
import type { Invite, InviteSort, InviteSortField } from './api'

// BINDS TO | CREATED | EXPIRES | CREATED BY | STATUS | NOTE.
//
// Against the hi-fi's header row (hifi3-holo-pages.jsx:2096):
//  - TOKEN PREFIX is DROPPED. Only tokenhash.Hash(rawHex) is stored
//    (internal/api/invites.go:56) and the list query cannot select it - omitting
//    i.token_hash from the projection IS the endpoint's security control
//    (internal/store/query/invites.sql:22-25). A prefix column would mean
//    persisting a fragment of a secret for cosmetics.
//  - CREATED BY is KEPT and filled with an EMAIL, because this list query joins
//    users (invites.sql:32). This is the one hi-fi column the enrollments table
//    could not fill; the bare created_by UUID is never rendered.
//  - CREATED is ADDED because it is the default sort key and needs a clickable
//    header (same reason as EnrollmentsTable.tsx:15).
//  - ACTIONS is renamed NOTE. The cell holds prose in the hi-fi too
//    (:2119-2121), and a header promising actions while delivering a sentence is
//    itself a dead affordance. There is no revoke, delete or resend route.
//
// Sortable headers ship even though the hi-fi has no sort control on this page:
// the endpoint supports both keys in both directions, Table makes the headers
// free, and the sketch's omission is a fidelity gap rather than a constraint.
const COLS = 'grid-cols-[1.5fr_110px_110px_1.4fr_110px_1fr]'

const HEADERS: TableColumn<InviteSortField>[] = [
  { label: 'BINDS TO' },
  { label: 'CREATED', field: 'created_at' },
  { label: 'EXPIRES', field: 'expires_at' },
  { label: 'CREATED BY' },
  { label: 'STATUS' },
  { label: 'NOTE', align: 'right' },
]

interface InvitesTableProps {
  invites: Invite[]
  sort: InviteSort
  onSort: (field: InviteSortField) => void
  // Injected so the pill and the relative label are pure functions of props. The
  // tab supplies useNow(60_000); tests supply a fixed Date.
  now: Date
}

export function InvitesTable({ invites, sort, onSort, now }: InvitesTableProps) {
  return (
    <div className="rounded-card border border-border bg-gradient-to-b from-white/[0.06] to-white/[0.02] backdrop-blur-[8px]">
      <Table
        label="Invites"
        columns={COLS}
        headers={HEADERS}
        sort={sort}
        onSort={onSort}
        headerClassName="px-[18px] py-3 tracking-[0.16em]"
      >
        {invites.map((inv) => {
          const status = deriveStatus(inv, now)
          const terminal = status === 'REDEEMED' || status === 'EXPIRED'
          return (
            <TableRow
              key={inv.id}
              className={`border-b border-accent/[0.06] px-[18px] py-2.5 font-mono text-[11.5px] ${
                terminal ? 'opacity-[0.55]' : ''
              }`}
            >
              {/* The key is ABSENT (not null) when the invite is not email-bound
                  (internal/api/invites.go:139-141), so this is a plain ASCII
                  hyphen placeholder - never an em dash - and it means "not bound
                  to an address", a real state rather than missing data. */}
              <TableCell className="truncate font-sans text-[12.5px] text-fg">
                {inv.email ?? <span className="text-fg-dim">-</span>}
              </TableCell>
              <TableCell className="text-[10.5px] text-fg-mute">
                {inv.created_at.slice(0, 10)}
              </TableCell>
              <TableCell
                className={`text-[11px] ${status === 'ACTIVE' ? 'text-fg' : 'text-fg-mute'}`}
              >
                {formatExpiryLabel(inv.expires_at, now)}
              </TableCell>
              <TableCell className="truncate text-[11px] text-fg-mute">
                {inv.created_by_email}
              </TableCell>
              <TableCell>
                <Chip tone={statusTone(status)}>{status}</Chip>
              </TableCell>
              {/* Prose, not controls. The only consumer of used_at's VALUE rather
                  than its presence. */}
              <TableCell className="text-right text-[10.5px] tracking-[0.04em] text-fg-dim">
                {status === 'REDEEMED'
                  ? `redeemed ${(inv.used_at ?? '').slice(0, 10)}`
                  : status === 'EXPIRED'
                    ? '-'
                    : 'copy token only on creation'}
              </TableCell>
            </TableRow>
          )
        })}
      </Table>
    </div>
  )
}
