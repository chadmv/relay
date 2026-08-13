import { useState } from 'react'
import { Button } from '../../components/Button'
import { GlassPanel, PillButton } from '../../components/holo'
import { computePageRange } from '../../lib/pageRange'
import { useNow } from '../../lib/useNow'
import { TokenRevealDialog } from '../TokenRevealDialog'
import { CreateInviteForm } from './CreateInviteForm'
import { InvitesTable } from './InvitesTable'
import { useInviteActions } from './useInviteActions'
import { useInvites } from './useInvites'
import type { CreateInviteBody, InviteSort, InviteSortField } from './api'

// Same shape as EnrollmentsTab's toggleSort (EnrollmentsTab.tsx:16-21): clicking
// the active column flips its direction, clicking the other selects it ascending.
//
// SEVENTH consumer of the cursor-pager block below (JobsPage, WorkersPage,
// SchedulesPage, UsersTab, EnrollmentsTab, ReservationsTab are the first six), and
// FOURTH of this helper. Not extracted here on purpose: the extraction has to
// migrate six shipped surfaces under a zero-line-diff gate on their existing test
// files, which is its own slice with a different risk profile. See
// docs/superpowers/plans/2026-08-13-admin-invites-tab.md, "Extraction debt".
function toggleSort(field: InviteSortField, current: InviteSort): InviteSort {
  if (current.replace('-', '') === field) {
    return (current.startsWith('-') ? field : `-${field}`) as InviteSort
  }
  return field
}

export function InvitesTab() {
  const [sort, setSort] = useState<InviteSort>('-created_at')
  // Cursor of the current page (''=first); stack holds the cursors we paged
  // forward from; offsets tracks the real row offset so partial pages stay
  // correct. Same pattern as UsersTab / EnrollmentsTab.
  const [cursor, setCursor] = useState('')
  const [stack, setStack] = useState<string[]>([])
  const [startOffset, setStartOffset] = useState(0)
  const [offsets, setOffsets] = useState<number[]>([])
  const [creating, setCreating] = useState(false)

  // A local 60s clock tick, NOT a poll: it re-renders so relative labels and
  // status pills stay correct and issues no request.
  const now = useNow(60_000)

  const { data, error, isLoading, isPlaceholderData, refetch } = useInvites(sort, cursor)
  const { create } = useInviteActions()

  function resetPaging() {
    setCursor('')
    setStack([])
    setStartOffset(0)
    setOffsets([])
  }

  function pickSort(field: InviteSortField) {
    setSort(toggleSort(field, sort))
    // The server rejects a cursor whose sort key does not match
    // (internal/api/pagination.go:272-286).
    resetPaging()
  }

  function next() {
    if (!data?.next_cursor) return
    const currentPageSize = data.items.length
    setStack([...stack, cursor])
    setCursor(data.next_cursor)
    setOffsets([...offsets, startOffset])
    setStartOffset(startOffset + currentPageSize)
  }

  function prev() {
    if (stack.length === 0) return
    const copy = [...stack]
    const back = copy.pop() ?? ''
    setStack(copy)
    setCursor(back)
    const offsetsCopy = [...offsets]
    const prevOffset = offsetsCopy.pop() ?? 0
    setOffsets(offsetsCopy)
    setStartOffset(prevOffset)
  }

  function onCreate(body: CreateInviteBody) {
    // The reveal dialog is driven by create.data, so closing the panel here is all
    // that is needed on success; the hook's onSuccess does the invalidation. This
    // is a MUTATE-level callback (fired by MutationObserver after the success
    // dispatch, query-core mutationObserver.js:85-95), not the hook-level one, so
    // it cannot interfere with the success notification.
    create.mutate(body, { onSuccess: () => setCreating(false) })
  }

  const invites = data?.items ?? []
  const total = data?.total ?? 0
  const { x, y } = computePageRange(startOffset, invites.length)
  const rangeText =
    invites.length === 0
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
  } else if (invites.length === 0) {
    // No prev escape hatch here, matching UsersTab rather than EnrollmentsTab.
    // That hatch exists there because the enrollments list is FILTERED
    // (consumed_at IS NULL AND expires_at > NOW()), so a row can vanish between
    // paging forward and the next fetch. This list applies no filter and nothing
    // deletes or reaps an invite, so a non-first page landing on zero rows is
    // unreachable - the hatch would be untestable dead code.
    body = (
      <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center text-[13px] text-fg-mute">
        No invites yet.
      </GlassPanel>
    )
  } else {
    body = (
      <>
        <InvitesTable invites={invites} sort={sort} onSort={pickSort} now={now} />
        <div className="flex items-center justify-between px-1 font-mono text-[10.5px] tracking-wider text-fg-mute">
          <span>
            SHOWING <span className="text-fg">{rangeText}</span>
            {' · '}/v1/invites (all states) · CURSOR PAGINATED
          </span>
          <div className="flex gap-2">
            <button
              type="button"
              onClick={prev}
              disabled={stack.length === 0 || isPlaceholderData}
              className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
            >
              ← prev
            </button>
            <button
              type="button"
              onClick={next}
              disabled={!data?.next_cursor || isPlaceholderData}
              className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
            >
              next 50 →
            </button>
          </div>
        </div>
      </>
    )
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-3">
        <span className="font-mono text-[11px] tracking-[0.06em] text-fg-mute">
          GET /v1/invites
        </span>
        <PillButton
          variant="primary"
          className="ml-auto"
          // Disabled while pending for the same reason as the panel's own Cancel
          // (CreateInviteForm.tsx): this toggle's onClick also calls
          // create.reset(), which only detaches the mutation observer and does
          // not cancel the in-flight request. Disabling it here additionally
          // closes a second-click accident: without this, a click mid-request
          // both reset() the live mutation and re-opened a fresh form, letting
          // the admin fire a duplicate create in two clicks.
          disabled={create.isPending}
          onClick={() => {
            // reset() clears a stale error AND, critically, a stale token: a
            // previous create's data would otherwise re-open the reveal dialog.
            create.reset()
            setCreating((v) => !v)
          }}
        >
          + Create invite
        </PillButton>
      </div>

      {creating && (
        <CreateInviteForm
          pending={create.isPending}
          error={create.error as Error | null}
          onSubmit={onCreate}
          onCancel={() => {
            create.reset()
            setCreating(false)
          }}
        />
      )}

      {body}

      <div className="font-mono text-[10px] leading-relaxed tracking-[0.04em] text-fg-dim">
        ▸ Invites are <span className="text-fg-mute">one-time</span>. The server returns the raw
        token only at creation, and there is no revoke endpoint in v1, so expiry or redemption are
        the only terminal states - prefer a short TTL. Email binding pins the invite to one address;
        an unbound invite can be redeemed by whoever holds the token. This list shows{' '}
        <span className="text-fg-mute">all states</span>, and the STATUS pill is derived in the
        browser from expires_at and used_at.
      </div>

      {/* Opens iff the mutation holds a result. The token is read straight from
          create.data and is never copied into state, so this is its only render
          site, and Done -> create.reset() both clears it and unmounts the dialog
          in one step: ending the generation IS releasing the resource, not a
          separate step that could be skipped. reset() must never move into the
          hook's onSuccess - query-core dispatches success only AFTER awaiting it
          (mutation.js:123 vs :144), so the detached observer would never see the
          success and this dialog would silently stop opening. */}
      {create.data && (
        <TokenRevealDialog
          token={create.data.token}
          title="Invite created"
          endpoint="POST /v1/invites"
          expiresAt={create.data.expires_at}
          onDone={() => create.reset()}
        />
      )}
    </div>
  )
}
