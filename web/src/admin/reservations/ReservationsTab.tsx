import { useState } from 'react'
import { Button } from '../../components/Button'
import { ConfirmDialog } from '../../components/ConfirmDialog'
import { GlassPanel, PillButton } from '../../components/holo'
import { computePageRange } from '../../lib/pageRange'
import { useNow } from '../../lib/useNow'
import { CreateReservationForm } from './CreateReservationForm'
import { ReservationsTable } from './ReservationsTable'
import { useReservationActions } from './useReservationActions'
import { useReservations } from './useReservations'
import type {
  CreateReservationBody,
  Reservation,
  ReservationSort,
  ReservationSortField,
} from './api'

// Same shape as EnrollmentsTab's toggleSort (EnrollmentsTab.tsx:16-21): clicking the
// active column flips its direction, clicking another selects it ascending.
function toggleSort(field: ReservationSortField, current: ReservationSort): ReservationSort {
  if (current.replace('-', '') === field) {
    return (current.startsWith('-') ? field : `-${field}`) as ReservationSort
  }
  return field
}

export function ReservationsTab() {
  const [sort, setSort] = useState<ReservationSort>('-created_at')
  // Cursor of the current page (''=first); stack holds the cursors we paged forward
  // from; offsets tracks the real row offset so partial pages stay correct. Same
  // pattern as EnrollmentsTab / UsersTab / JobsPage.
  const [cursor, setCursor] = useState('')
  const [stack, setStack] = useState<string[]>([])
  const [startOffset, setStartOffset] = useState(0)
  const [offsets, setOffsets] = useState<number[]>([])
  const [creating, setCreating] = useState(false)
  const [confirm, setConfirm] = useState<Reservation | null>(null)

  // A local 60s clock tick, NOT a poll: it re-renders so the derived STATUS pill
  // stays correct as a window opens or closes, and issues no request.
  const now = useNow(60_000)

  const { data, error, isLoading, isPlaceholderData, refetch } = useReservations(sort, cursor)
  const { create, remove } = useReservationActions()

  // create.error is routed into the panel (it owns that copy); delete errors land in
  // the shared box, matching UsersTab.tsx:53-60.
  const actionError = remove.error as Error | null

  function resetPaging() {
    setCursor('')
    setStack([])
    setStartOffset(0)
    setOffsets([])
  }

  function pickSort(field: ReservationSortField) {
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

  function onCreate(body: CreateReservationBody) {
    create.mutate(body, { onSuccess: () => setCreating(false) })
  }

  function runConfirmed() {
    if (!confirm) return
    remove.mutate(confirm.id)
    setConfirm(null)
  }

  const reservations = data?.items ?? []
  const total = data?.total ?? 0
  const { x, y } = computePageRange(startOffset, reservations.length)
  const rangeText =
    reservations.length === 0
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
  } else if (reservations.length === 0) {
    // A concurrent delete can empty a non-first page; without a way back, a reload is
    // the admin's only exit (same escape hatch as EnrollmentsTab.tsx:113-130).
    body = (
      <>
        <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center text-[13px] text-fg-mute">
          No reservations.
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
        <ReservationsTable
          reservations={reservations}
          sort={sort}
          onSort={pickSort}
          now={now}
          busy={remove.isPending}
          onDelete={(r) => {
            // Clear a stale error before opening for a (possibly different) row -
            // the reset()-before-open convention from UsersTab.tsx:173-179.
            remove.reset()
            setConfirm(r)
          }}
        />
        <div className="flex items-center justify-between px-1 font-mono text-[10.5px] tracking-wider text-fg-mute">
          <span>
            SHOWING <span className="text-fg">{rangeText}</span>
            {' · '}/v1/reservations · CURSOR PAGINATED
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
          GET /v1/reservations
        </span>
        <PillButton
          variant="primary"
          className="ml-auto"
          onClick={() => {
            // reset() clears a stale error so a freshly reopened empty form never
            // shows a leftover message (UsersTab.tsx:238-245).
            create.reset()
            setCreating((v) => !v)
          }}
        >
          + Reserve workers
        </PillButton>
      </div>

      {creating && (
        <CreateReservationForm
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

      {/* This footnote CORRECTS the hi-fi rather than repeating it. The hi-fi's
          framing implies reserved workers run their owner's work; the scheduler
          unions worker_ids into reservedIDs and skips those workers for EVERY task
          (internal/scheduler/dispatch.go:185-191, :221-223). Kept as plain text with
          no nested spans so the wording is assertable as one normalized string. */}
      <div className="font-mono text-[10px] leading-relaxed tracking-[0.04em] text-fg-dim">
        ▸ A reservation removes its worker_ids from the general dispatch pool: those workers stop
        receiving new tasks from every job, including the owner's own jobs. relay does not route
        the owner's work to them. selector, project and the owner are recorded but never read by
        the scheduler; only explicit worker_ids are enforced. A reservation is in force only while
        starts_at &lt;= now &lt; ends_at, and either bound may be open. Changes take effect on the
        next dispatch cycle (about 30s) and never preempt a task that is already running.
      </div>

      {confirm && (
        <ConfirmDialog
          title={`Delete reservation "${confirm.name}"?`}
          body={`Deleting returns its ${confirm.worker_ids.length} worker(s) to the general dispatch pool on the next dispatch cycle (about 30s). Tasks already running on them are unaffected. This cannot be undone.`}
          confirmLabel="Delete"
          destructive
          onConfirm={runConfirmed}
          onCancel={() => setConfirm(null)}
        />
      )}
    </div>
  )
}
