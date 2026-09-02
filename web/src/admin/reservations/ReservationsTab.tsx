import { useState } from 'react'
import { Button } from '../../components/Button'
import { ConfirmDialog } from '../../components/ConfirmDialog'
import { GlassPanel, PillButton } from '../../components/holo'
import { computePageRange } from '../../lib/pageRange'
import { toggleSort } from '../../lib/toggleSort'
import { useCursorPager } from '../../lib/useCursorPager'
import { useNow } from '../../lib/useNow'
import { CreateReservationForm } from './CreateReservationForm'
import { deriveStatus } from './reservationStatus'
import { ReservationsTable } from './ReservationsTable'
import { useReservationActions } from './useReservationActions'
import { useReservations } from './useReservations'
import type {
  CreateReservationBody,
  Reservation,
  ReservationSort,
  ReservationSortField,
} from './api'

// M2 (review 2026-08-09): the delete confirm body used to be unconditional -
// "returns its N worker(s) to the general dispatch pool" - regardless of whether the
// row was ever actually withheld. Only an ACTIVE row (deriveStatus, which already
// mirrors ListActiveReservations exactly) with at least one worker was ever in the
// dispatcher's reservedIDs set; a SCHEDULED or ENDED row's workers were never
// withheld, and worker_ids: [] withholds nothing regardless of status, so deleting
// either changes nothing about dispatch - it only removes the record.
function confirmDeleteBody(r: Reservation, now: Date): string {
  const workerCount = r.worker_ids.length
  const status = deriveStatus(r, now)
  const tail = 'It only removes the record. This cannot be undone.'
  if (workerCount === 0) {
    return `This reservation holds no workers, so deleting it does not change dispatch. ${tail}`
  }
  if (status !== 'ACTIVE') {
    return `This reservation is not currently in force (${status.toLowerCase()}), so deleting it does not change dispatch - its workers are not currently withheld from the pool. ${tail}`
  }
  return `Deleting returns its ${workerCount} worker(s) to the general dispatch pool on the next dispatch cycle (about 30s). Tasks already running on them are unaffected. This cannot be undone.`
}

export function ReservationsTab() {
  const [sort, setSort] = useState<ReservationSort>('-created_at')
  const pager = useCursorPager()
  const [creating, setCreating] = useState(false)
  const [confirm, setConfirm] = useState<Reservation | null>(null)

  // A local 60s clock tick, NOT a poll: it re-renders so the derived STATUS pill
  // stays correct as a window opens or closes, and issues no request.
  const now = useNow(60_000)

  const { data, error, isLoading, isPlaceholderData, refetch } = useReservations(sort, pager.cursor)
  const { create, remove } = useReservationActions()

  // create.error is routed into the panel (it owns that copy); delete errors land in
  // the shared box, matching UsersTab.tsx's actionError.
  const actionError = remove.error as Error | null

  function pickSort(field: ReservationSortField) {
    setSort(toggleSort(field, sort))
    // The server rejects a cursor whose sort key does not match
    // (internal/api/pagination.go:272-286).
    pager.resetPaging()
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
  const { x, y } = computePageRange(pager.startOffset, reservations.length)
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
    // the admin's only exit (same escape hatch as EnrollmentsTab.tsx's active-only
    // empty state, in its `enrollments.length === 0` branch).
    body = (
      <>
        <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center text-[13px] text-fg-mute">
          No reservations.
        </GlassPanel>
        {pager.canPrev && (
          <div className="flex justify-center">
            <button
              type="button"
              onClick={pager.prev}
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
            // the reset()-before-open convention from UsersTab.tsx (resetPassword.reset()
            // before setResetting).
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
            // shows a leftover message (UsersTab.tsx's create.reset()-before-toggle
            // convention).
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
          body={confirmDeleteBody(confirm, now)}
          confirmLabel="Delete"
          destructive
          onConfirm={runConfirmed}
          onCancel={() => setConfirm(null)}
        />
      )}
    </div>
  )
}
