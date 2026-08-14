import { useState } from 'react'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { PillButton } from '../components/holo'
import { useJobActions } from './useJobActions'
import type { JobDetail } from './api'

type Pending = null | 'cancel' | 'force' | 'retry-failed' | 'retry-all'

// Job-detail header action bar. Owns the cancel pair, the retry pair, the confirm
// dialog, and the inline error. A cancelled or retried job stays viewable, so on
// success we do NOT navigate; the ['job', id] invalidation flips the status pill on
// refetch.
export function JobActions({ job }: { job: JobDetail }) {
  const { cancel, retry } = useJobActions(job.id)
  const [confirm, setConfirm] = useState<Pending>(null)

  // Hide the buttons only for states the server treats as terminal for cancel
  // (cancelled/done). `failed` is NOT terminal server-side, so it stays
  // cancellable and keeps its buttons.
  const terminal = job.status === 'cancelled' || job.status === 'done'

  // Retry availability, spelled as an ALLOW-LIST of the exact two statuses
  // handleRetryJob admits. Everything else - pending, running, cancelled - is a
  // deterministic 409 from its status switch, and a control that is always visible
  // and always fails is a dead control. The allow-list spelling is not cosmetic:
  // a status added to JobStatus later must be un-retryable until somebody decides
  // otherwise, which is the frontend reading of the rule RetryJobTasks's own
  // status predicate follows. A deny-list here would fail open.
  const retryable = job.status === 'done' || job.status === 'failed'

  const actionError = cancel.error as Error | null

  function openConfirm(which: Exclude<Pending, null>) {
    // Both mutations are reset, so a stale banner from the OTHER action cannot
    // outlive the next confirm.
    cancel.reset()
    retry.reset()
    setConfirm(which)
  }

  function runConfirmed() {
    if (confirm === 'cancel') cancel.mutate(false)
    else if (confirm === 'force') cancel.mutate(true)
    else if (confirm === 'retry-failed') retry.mutate('failed')
    else if (confirm === 'retry-all') retry.mutate('all')
    // The dialog closes BEFORE the response lands, on purpose: the error banner
    // below lives on the page, and an open dialog would put its scrim on top of
    // it, so the button would look like it did nothing. Do not "improve" this by
    // holding the dialog open while the request is in flight.
    setConfirm(null)
  }

  const confirmCopy: Record<Exclude<Pending, null>, { title: string; body: string; label: string; destructive?: boolean }> = {
    cancel: {
      title: `Cancel ${job.name}?`,
      body: 'Running tasks are asked to stop and the job is marked cancelled. Tasks that have not started are dropped.',
      // "Cancel job" (not "Cancel") avoids ambiguity with the dialog's own
      // "Cancel" dismiss button.
      label: 'Cancel job',
      destructive: true,
    },
    force: {
      title: `Force cancel ${job.name}?`,
      body: 'Running tasks are force-killed immediately and the job is marked cancelled. Use this when a graceful cancel is not stopping the work.',
      label: 'Force cancel',
      destructive: true,
    },
    'retry-failed': {
      title: `Retry failed tasks of ${job.name}?`,
      body: 'Every task that failed or timed out is queued again and the job goes back to running. Tasks that already succeeded are left alone.',
      // Distinct from the pill label, like "Cancel job" above, so a test (and a
      // screen reader) can tell the trigger from the confirmation.
      label: 'Retry failed tasks',
    },
    'retry-all': {
      title: `Retry all tasks of ${job.name}?`,
      body: 'Every finished task is queued again, including the ones that already succeeded, and the job goes back to running. This re-runs work that is already done and spends farm capacity on it.',
      label: 'Retry all tasks',
    },
  }

  return (
    <div className="flex flex-col gap-2">
      {(retryable || !terminal) && (
        <div className="flex items-center gap-2">
          {retryable && (
            <>
              <PillButton variant="ghost" disabled={retry.isPending} onClick={() => openConfirm('retry-failed')}>
                Retry failed
              </PillButton>
              <PillButton variant="ghost" disabled={retry.isPending} onClick={() => openConfirm('retry-all')}>
                Retry all
              </PillButton>
            </>
          )}
          {!terminal && (
            <>
              <PillButton variant="ghost" disabled={cancel.isPending} onClick={() => openConfirm('cancel')}>
                Cancel
              </PillButton>
              <PillButton variant="danger" disabled={cancel.isPending} onClick={() => openConfirm('force')}>
                Force cancel
              </PillButton>
            </>
          )}
        </div>
      )}

      {actionError ? (
        <div className="rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err">
          {actionError.message}
        </div>
      ) : null}

      {confirm && (
        <ConfirmDialog
          title={confirmCopy[confirm].title}
          body={confirmCopy[confirm].body}
          confirmLabel={confirmCopy[confirm].label}
          destructive={confirmCopy[confirm].destructive}
          onConfirm={runConfirmed}
          onCancel={() => setConfirm(null)}
        />
      )}
    </div>
  )
}
