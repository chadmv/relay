import { useState } from 'react'
import { Chip, Table, TableCell, TableRow, type TableColumn } from '../../components/holo'
import { Input } from '../../components/Input'
import type { AdminUser, UserSort, UserSortField } from './api'

// EMAIL | NAME | ROLE | CREATED | ACTIONS. The hi-fi's SESSIONS and LAST LOGIN
// columns are omitted: no endpoint exposes a per-user token count and `users` has
// no last_login_at column. Faking either would read as real data.
const COLS = 'grid-cols-[1.6fr_1fr_110px_120px_270px]'

const HEADERS: TableColumn<UserSortField>[] = [
  { label: 'EMAIL', field: 'email' },
  { label: 'NAME', field: 'name' },
  { label: 'ROLE' },
  { label: 'CREATED', field: 'created_at' },
  { label: 'ACTIONS', align: 'right' },
]

// Row mini-actions use literal classes rather than PillButton overrides: two
// competing padding utilities on one element resolve by stylesheet order, not by
// class-attribute order, so an override is not reliable at this size.
const MINI = 'rounded-full border px-2.5 py-1 font-mono text-[10.5px] tracking-[0.04em] disabled:opacity-40'
const MINI_GHOST = `${MINI} border-border bg-white/5 text-fg-mute`
const MINI_ACCENT = `${MINI} border-accent/50 bg-accent/10 text-accent`
const MINI_DANGER = `${MINI} border-err/40 bg-err/10 text-err`

interface UsersTableProps {
  users: AdminUser[]
  sort: UserSort
  onSort: (field: UserSortField) => void
  // Only when the include-archived toggle is ON is archived_at meaningful: the
  // active-only query family sends a zero timestamp (internal/api/users.go:111-132).
  showArchived: boolean
  currentUserId: string
  busy: boolean
  onRename: (id: string, name: string) => void
  onResetPassword: (user: AdminUser) => void
  onArchive: (user: AdminUser) => void
  onUnarchive: (user: AdminUser) => void
}

export function UsersTable({
  users,
  sort,
  onSort,
  showArchived,
  currentUserId,
  busy,
  onRename,
  onResetPassword,
  onArchive,
  onUnarchive,
}: UsersTableProps) {
  const [editingId, setEditingId] = useState<string | null>(null)
  const [draft, setDraft] = useState('')

  function startRename(u: AdminUser) {
    setEditingId(u.id)
    setDraft(u.name)
  }

  function submitRename(id: string) {
    const name = draft.trim()
    if (!name) return
    onRename(id, name)
    setEditingId(null)
  }

  return (
    <div className="rounded-card border border-border bg-gradient-to-b from-white/[0.06] to-white/[0.02] backdrop-blur-[8px]">
      <Table
        label="Users"
        columns={COLS}
        headers={HEADERS}
        sort={sort}
        onSort={onSort}
        headerClassName="px-[18px] py-3 tracking-[0.16em]"
      >
        {users.map((u) => {
          const archived = showArchived && Boolean(u.archived_at)
          const isSelf = u.id === currentUserId
          return (
            <TableRow
              key={u.id}
              className={`border-b border-accent/[0.06] px-[18px] py-2.5 font-mono text-[11.5px] ${
                archived ? 'opacity-[0.55]' : ''
              }`}
            >
              <TableCell className="flex min-w-0 items-center gap-2.5">
                <span className="grid h-6 w-6 flex-none place-items-center rounded-md bg-gradient-to-br from-accent/45 to-accent-b/30 text-[11px] font-semibold text-white">
                  {u.email.charAt(0).toUpperCase()}
                </span>
                <span className="truncate font-sans text-[12.5px] text-fg">{u.email}</span>
              </TableCell>

              <TableCell className="min-w-0 pr-2">
                {editingId === u.id ? (
                  <span className="flex items-center gap-1.5">
                    <Input
                      aria-label={`Name for ${u.email}`}
                      value={draft}
                      onChange={(e) => setDraft(e.target.value)}
                      className="py-1 text-[12px]"
                    />
                    <button type="button" className={MINI_ACCENT} onClick={() => submitRename(u.id)}>
                      Save
                    </button>
                    <button type="button" className={MINI_GHOST} onClick={() => setEditingId(null)}>
                      Cancel
                    </button>
                  </span>
                ) : (
                  <span className="truncate font-sans text-[12px] text-fg-mute">{u.name}</span>
                )}
              </TableCell>

              <TableCell>
                {/* Two values only. Relay's model is a single is_admin boolean; the
                    hi-fi's `service` role is mock fiction. */}
                <Chip tone={u.is_admin ? 'accent' : 'muted'}>{u.is_admin ? 'ADMIN' : 'USER'}</Chip>
              </TableCell>

              <TableCell className="text-[10.5px] text-fg-mute">{u.created_at.slice(0, 10)}</TableCell>

              <TableCell className="flex justify-end gap-1.5">
                {archived ? (
                  // No Unarchive on your own archived row either: the server 400s
                  // "cannot unarchive yourself" (symmetric with the Archive guard
                  // below), so the button would be a guaranteed-failing control.
                  !isSelf && (
                    <button
                      type="button"
                      className={MINI_ACCENT}
                      disabled={busy}
                      aria-label={`Unarchive ${u.email}`}
                      onClick={() => onUnarchive(u)}
                    >
                      Unarchive
                    </button>
                  )
                ) : (
                  <>
                    <button
                      type="button"
                      className={MINI_GHOST}
                      disabled={busy}
                      aria-label={`Reset password for ${u.email}`}
                      onClick={() => onResetPassword(u)}
                    >
                      Reset pw
                    </button>
                    <button
                      type="button"
                      className={MINI_GHOST}
                      disabled={busy}
                      aria-label={`Rename ${u.email}`}
                      onClick={() => startRename(u)}
                    >
                      Rename
                    </button>
                    {/* No Archive on your own row: the server 400s "cannot archive
                        yourself", so the button would be a guaranteed-failing control. */}
                    {!isSelf && (
                      <button
                        type="button"
                        className={MINI_DANGER}
                        disabled={busy}
                        aria-label={`Archive ${u.email}`}
                        onClick={() => onArchive(u)}
                      >
                        Archive
                      </button>
                    )}
                  </>
                )}
              </TableCell>
            </TableRow>
          )
        })}
      </Table>
    </div>
  )
}
