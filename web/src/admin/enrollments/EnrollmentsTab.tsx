import { useState } from 'react'
import { Button } from '../../components/Button'
import { GlassPanel, PillButton } from '../../components/holo'
import { computePageRange } from '../../lib/pageRange'
import { useNow } from '../../lib/useNow'
import { TokenRevealDialog } from '../TokenRevealDialog'
import { CreateEnrollmentForm } from './CreateEnrollmentForm'
import { EnrollmentsTable } from './EnrollmentsTable'
import { useAgentEnrollmentActions } from './useAgentEnrollmentActions'
import { useAgentEnrollments } from './useAgentEnrollments'
import type { CreateEnrollmentBody, EnrollmentSort, EnrollmentSortField } from './api'

// Same shape as UsersTab's toggleSort (web/src/admin/users/UsersTab.tsx:17-22):
// clicking the active column flips its direction, clicking the other selects it
// ascending.
function toggleSort(field: EnrollmentSortField, current: EnrollmentSort): EnrollmentSort {
  if (current.replace('-', '') === field) {
    return (current.startsWith('-') ? field : `-${field}`) as EnrollmentSort
  }
  return field
}

export function EnrollmentsTab() {
  const [sort, setSort] = useState<EnrollmentSort>('-created_at')
  // Cursor of the current page (''=first); stack holds the cursors we paged
  // forward from; offsets tracks the real row offset so partial pages stay
  // correct. Same pattern as UsersTab / JobsPage.
  const [cursor, setCursor] = useState('')
  const [stack, setStack] = useState<string[]>([])
  const [startOffset, setStartOffset] = useState(0)
  const [offsets, setOffsets] = useState<number[]>([])
  const [creating, setCreating] = useState(false)

  // A local 60s clock tick, NOT a poll: it re-renders so relative labels and
  // status pills stay correct and issues no request.
  const now = useNow(60_000)

  const { data, error, isLoading, isPlaceholderData, refetch } = useAgentEnrollments(sort, cursor)
  const { create } = useAgentEnrollmentActions()

  function resetPaging() {
    setCursor('')
    setStack([])
    setStartOffset(0)
    setOffsets([])
  }

  function pickSort(field: EnrollmentSortField) {
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

  function onCreate(body: CreateEnrollmentBody) {
    // The reveal dialog is driven by create.data, so closing the panel here is all
    // that is needed on success; the hook's onSuccess does the invalidation.
    create.mutate(body, { onSuccess: () => setCreating(false) })
  }

  const enrollments = data?.items ?? []
  const total = data?.total ?? 0
  const { x, y } = computePageRange(startOffset, enrollments.length)
  const rangeText =
    enrollments.length === 0
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
  } else if (enrollments.length === 0) {
    // Unlike UsersTab, this list is active-only: a row can be consumed or expire
    // between paging forward and the next fetch, so a non-first page landing on
    // zero rows is reachable in normal use, not just a data-corruption edge case.
    // Without a way back here, a reload is the admin's only exit.
    body = (
      <>
        <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center text-[13px] text-fg-mute">
          No active enrollments.
        </GlassPanel>
        {stack.length > 0 && (
          <div className="flex justify-center">
            <button
              type="button"
              onClick={prev}
              className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute"
            >
              ← prev
            </button>
          </div>
        )}
      </>
    )
  } else {
    body = (
      <>
        <EnrollmentsTable enrollments={enrollments} sort={sort} onSort={pickSort} now={now} />
        <div className="flex items-center justify-between px-1 font-mono text-[10.5px] tracking-wider text-fg-mute">
          <span>
            SHOWING <span className="text-fg">{rangeText}</span>
            {' · '}/v1/agent-enrollments (active only) · CURSOR PAGINATED
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
          GET /v1/agent-enrollments
        </span>
        <PillButton
          variant="primary"
          className="ml-auto"
          // Disabled while pending for the same reason as the panel's own Cancel
          // (CreateEnrollmentForm.tsx): this toggle's onClick also calls
          // create.reset(), which only detaches the mutation observer and does
          // not cancel the in-flight request. Disabling it here additionally
          // closes a second-click accident: without this, a click mid-request
          // both reset() the live mutation and re-opened a fresh form, letting
          // the admin fire a duplicate enroll in two clicks.
          disabled={create.isPending}
          onClick={() => {
            // reset() clears a stale error AND, critically, a stale token: a
            // previous create's data would otherwise re-open the reveal dialog.
            create.reset()
            setCreating((v) => !v)
          }}
        >
          + Enroll agent
        </PillButton>
      </div>

      {creating && (
        <CreateEnrollmentForm
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
        ▸ Enrollments bootstrap a <span className="text-fg-mute">relay-agent</span>: set the token
        as <span className="text-fg-mute">RELAY_AGENT_ENROLLMENT_TOKEN</span> on first boot, and the
        agent exchanges it for a long-lived agent token. Single use. This list shows{' '}
        <span className="text-fg-mute">active only</span>, so a consumed or expired enrollment
        disappears rather than changing state. There is no revoke endpoint in v1, so expiry or
        consumption are the only terminal states - prefer a short TTL. A worker that already
        enrolled can be cut off with DELETE /v1/workers/{'{id}'}/token.
      </div>

      {/* Opens iff the mutation holds a result. The token is read straight from
          create.data and is never copied into state, so this is its only render
          site, and Done -> create.reset() both clears it and unmounts the dialog
          in one step. */}
      {create.data && (
        <TokenRevealDialog
          token={create.data.token}
          title="Agent enrollment created"
          endpoint="POST /v1/agent-enrollments"
          expiresAt={create.data.expires_at}
          onDone={() => create.reset()}
        />
      )}
    </div>
  )
}
