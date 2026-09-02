import { useState } from 'react'
import { useAuth } from '../../auth/AuthProvider'
import { Button } from '../../components/Button'
import { ConfirmDialog } from '../../components/ConfirmDialog'
import { GlassPanel, PillButton } from '../../components/holo'
import { computePageRange } from '../../lib/pageRange'
import { toggleSort } from '../../lib/toggleSort'
import { useCursorPager } from '../../lib/useCursorPager'
import { useDebouncedValue } from '../../lib/useDebouncedValue'
import { CreateUserForm } from './CreateUserForm'
import { ResetPasswordDialog } from './ResetPasswordDialog'
import { UsersTable } from './UsersTable'
import { useAdminUserActions } from './useAdminUserActions'
import { useAdminUsers } from './useAdminUsers'
import type { AdminUser, CreateUserBody, UserSort, UserSortField } from './api'

type Confirm = { kind: 'archive' | 'unarchive'; user: AdminUser } | null

// debounceMs is a prop only so tests can shrink it and stay on real timers;
// production always uses the 300ms default.
export function UsersTab({ debounceMs = 300 }: { debounceMs?: number }) {
  const { user: me } = useAuth()
  const [sort, setSort] = useState<UserSort>('-created_at')
  const [includeArchived, setIncludeArchived] = useState(false)
  const [emailInput, setEmailInput] = useState('')
  const email = useDebouncedValue(emailInput, debounceMs).trim()
  const pager = useCursorPager()
  const [creating, setCreating] = useState(false)
  const [confirm, setConfirm] = useState<Confirm>(null)
  const [resetting, setResetting] = useState<AdminUser | null>(null)

  const { data, error, isLoading, isPlaceholderData, refetch } = useAdminUsers(
    sort,
    includeArchived,
    pager.cursor,
    email,
  )
  const { create, rename, archive, unarchive, resetPassword } = useAdminUserActions()

  // create.error is routed into CreateUserForm (it owns the 409 copy) and
  // resetPassword.error is routed into ResetPasswordDialog (its scrim sits above
  // this page-level box, so a stale reset error rendered only here would be
  // invisible while the dialog is open) - both are deliberately excluded from the
  // shared inline error box below.
  const actionError = (rename.error ?? archive.error ?? unarchive.error) as Error | null
  const busy =
    rename.isPending || archive.isPending || unarchive.isPending || resetPassword.isPending
  const filtering = email !== ''

  function pickSort(field: UserSortField) {
    setSort(toggleSort(field, sort))
    // The server rejects a cursor whose sort key does not match (internal/api/pagination.go).
    pager.resetPaging()
  }

  function pickIncludeArchived(v: boolean) {
    setIncludeArchived(v)
    // Different row set and total, so the old cursor is meaningless.
    pager.resetPaging()
  }

  function pickEmail(v: string) {
    setEmailInput(v)
    pager.resetPaging()
  }

  function onCreate(body: CreateUserBody) {
    create.mutate(body, { onSuccess: () => setCreating(false) })
  }

  function runConfirmed() {
    if (!confirm) return
    if (confirm.kind === 'archive') archive.mutate(confirm.user.id)
    else unarchive.mutate(confirm.user.id)
    setConfirm(null)
  }

  function onResetSubmit(newPassword: string) {
    if (!resetting) return
    resetPassword.mutate(
      { email: resetting.email, newPassword },
      { onSuccess: () => setResetting(null) },
    )
  }

  const users = data?.items ?? []
  const total = data?.total ?? 0
  const { x, y } = computePageRange(pager.startOffset, users.length)
  const rangeText =
    users.length === 0
      ? `0 of ${total.toLocaleString()}`
      : `${x.toLocaleString()}-${y.toLocaleString()} of ${total.toLocaleString()}`

  let body
  if (isLoading && !data) {
    body = (
      <div className="flex flex-col gap-2">
        {Array.from({ length: 8 }).map((_, i) => (
          <GlassPanel key={i} className="h-9" />
        ))}
      </div>
    )
  } else if (error && !data) {
    body = (
      <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center">
        <div className="mb-3 text-[13px] text-err">{(error as Error).message}</div>
        <Button className="w-auto px-4" onClick={() => refetch()}>
          Retry
        </Button>
      </GlassPanel>
    )
  } else if (users.length === 0) {
    body = (
      <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center text-[13px] text-fg-mute">
        {filtering ? 'No users match that email.' : 'No users yet.'}
      </GlassPanel>
    )
  } else {
    body = (
      <>
        <UsersTable
          users={users}
          sort={sort}
          onSort={pickSort}
          showArchived={includeArchived}
          currentUserId={me?.id ?? ''}
          busy={busy}
          onRename={(id, name) => rename.mutate({ id, name })}
          onResetPassword={(u) => {
            // Clear a stale error/plaintext-password from a previous reset attempt
            // before opening for a (possibly different) row - matches JobActions'
            // openConfirm convention (cancel.reset() before setConfirm).
            resetPassword.reset()
            setResetting(u)
          }}
          onArchive={(u) => setConfirm({ kind: 'archive', user: u })}
          onUnarchive={(u) => setConfirm({ kind: 'unarchive', user: u })}
        />
        <div className="flex items-center justify-between px-1 font-mono text-[10.5px] tracking-wider text-fg-mute">
          <span>
            SHOWING <span className="text-fg">{rangeText}</span>
            {' · '}/v1/users{filtering ? ' · EXACT EMAIL MATCH' : ' · CURSOR PAGINATED'}
          </span>
          {/* The server returns before parsePage on the ?email= branch, so while a
              filter is active there is no page to walk. */}
          {!filtering && (
            <div className="flex gap-2">
              <button
                type="button"
                onClick={pager.prev}
                disabled={!pager.canPrev || isPlaceholderData}
                className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
              >
                ← prev
              </button>
              <button
                type="button"
                onClick={() => pager.next(data)}
                disabled={!data?.next_cursor || isPlaceholderData}
                className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
              >
                next 50 →
              </button>
            </div>
          )}
        </div>
      </>
    )
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-3">
        <span className="font-mono text-[11px] tracking-[0.06em] text-fg-mute">GET /v1/users</span>
        <label className="flex cursor-pointer items-center gap-2 text-[12px] text-fg-mute">
          <input
            type="checkbox"
            checked={includeArchived}
            onChange={(e) => pickIncludeArchived(e.target.checked)}
            className="accent-accent"
          />
          include archived
          <span className="font-mono text-[11px] text-fg-dim">?include_archived=true</span>
        </label>
        <input
          aria-label="Filter by email"
          placeholder="?email=… exact, case-sensitive match"
          value={emailInput}
          onChange={(e) => pickEmail(e.target.value)}
          className="ml-auto min-w-[240px] rounded-full border border-border bg-black/25 px-3.5 py-1.5 text-[12px] text-fg outline-none placeholder:text-fg-dim focus:border-accent"
        />
        <PillButton
          variant="primary"
          onClick={() => {
            // Clear a stale error (e.g. a previous 409) before toggling, so a
            // freshly reopened empty form never shows a leftover message -
            // matches the convention at web/src/jobs/JobActions.tsx (cancel.reset())
            // and web/src/jobs/NewJobPage.tsx (create.reset()).
            create.reset()
            setCreating((v) => !v)
          }}
        >
          + Create user
        </PillButton>
      </div>

      {creating && (
        <CreateUserForm
          pending={create.isPending}
          error={create.error as Error | null}
          onSubmit={onCreate}
          onCancel={() => {
            create.reset()
            setCreating(false)
          }}
        />
      )}

      {actionError ? (
        <div className="rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err">
          {actionError.message}
        </div>
      ) : null}

      {body}

      <div className="font-mono text-[10px] leading-relaxed tracking-[0.04em] text-fg-dim">
        ▸ <span className="text-fg-mute">Archive</span> immediately revokes every API token for
        that user, forces re-login, and disables their scheduled jobs. Server guards prevent
        archiving yourself or the last active admin. Password reset revokes the target's sessions
        too.
      </div>

      {confirm && (
        <ConfirmDialog
          title={
            confirm.kind === 'archive'
              ? `Archive ${confirm.user.email}?`
              : `Unarchive ${confirm.user.email}?`
          }
          body={
            confirm.kind === 'archive'
              ? 'This revokes all of their API tokens, forces re-login, and disables their scheduled jobs.'
              : 'This restores their access. Their API tokens are not restored, so they must sign in again.'
          }
          confirmLabel={confirm.kind === 'archive' ? 'Archive' : 'Unarchive'}
          destructive={confirm.kind === 'archive'}
          onConfirm={runConfirmed}
          onCancel={() => setConfirm(null)}
        />
      )}

      {resetting && (
        <ResetPasswordDialog
          email={resetting.email}
          pending={resetPassword.isPending}
          error={resetPassword.error as Error | null}
          onSubmit={onResetSubmit}
          onCancel={() => setResetting(null)}
        />
      )}
    </div>
  )
}
