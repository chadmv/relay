import { Chip } from '../../components/holo'
import { formatTimeUntil } from '../../lib/time'
import { deriveStatus, statusTone } from './enrollmentStatus'
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
const COLS = 'grid grid-cols-[1.6fr_130px_130px_120px_1fr]'

function caret(field: EnrollmentSortField, sort: EnrollmentSort): string {
  if (sort.replace('-', '') !== field) return ''
  return sort.startsWith('-') ? ' ▼' : ' ▲'
}

function ariaSort(
  field: EnrollmentSortField,
  sort: EnrollmentSort,
): 'ascending' | 'descending' | 'none' {
  if (sort.replace('-', '') !== field) return 'none'
  return sort.startsWith('-') ? 'descending' : 'ascending'
}

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
    <div
      role="table"
      aria-label="Agent enrollments"
      className="rounded-card border border-border bg-gradient-to-b from-white/[0.06] to-white/[0.02] backdrop-blur-[8px]"
    >
      <div
        role="row"
        className={`${COLS} border-b border-border px-[18px] py-3 font-mono text-[10px] tracking-[0.16em] text-fg-mute`}
      >
        <span role="columnheader">HOSTNAME HINT</span>
        <div role="columnheader" aria-sort={ariaSort('created_at', sort)}>
          <button type="button" className="text-left" onClick={() => onSort('created_at')}>
            CREATED{caret('created_at', sort)}
          </button>
        </div>
        <div role="columnheader" aria-sort={ariaSort('expires_at', sort)}>
          <button type="button" className="text-left" onClick={() => onSort('expires_at')}>
            EXPIRES{caret('expires_at', sort)}
          </button>
        </div>
        <span role="columnheader">STATUS</span>
        <span role="columnheader" className="text-right">
          NOTE
        </span>
      </div>

      {enrollments.map((e) => {
        const status = deriveStatus(e.expires_at, now)
        return (
          <div
            key={e.id}
            role="row"
            className={`${COLS} items-center border-b border-accent/[0.06] px-[18px] py-2.5 font-mono text-[11.5px] ${
              status === 'EXPIRED' ? 'opacity-[0.55]' : ''
            }`}
          >
            {/* The key is ABSENT (not null) when unset
                (internal/api/agent_enrollments.go:90-92), so this is a plain
                ASCII hyphen placeholder - never an em dash. */}
            <span role="cell" className="truncate font-sans text-[12.5px] text-fg">
              {e.hostname_hint ?? <span className="text-fg-dim">-</span>}
            </span>
            <span role="cell" className="text-[10.5px] text-fg-mute">
              {e.created_at.slice(0, 10)}
            </span>
            <span
              role="cell"
              className={`text-[11px] ${status === 'ACTIVE' ? 'text-fg' : 'text-fg-mute'}`}
            >
              {formatTimeUntil(e.expires_at, now)}
            </span>
            <span role="cell">
              <Chip tone={statusTone(status)}>{status}</Chip>
            </span>
            <span role="cell" className="text-right text-[10.5px] tracking-[0.04em] text-fg-dim">
              consumed on first agent connect
            </span>
          </div>
        )
      })}
    </div>
  )
}
