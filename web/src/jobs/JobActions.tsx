import { useState } from 'react'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { PillButton } from '../components/holo'
import { useJobActions } from './useJobActions'
import type { JobDetail } from './api'

type Pending = null | 'cancel' | 'force'

// Job-detail header action bar. Owns the two cancel buttons, the confirm dialog,
// and the inline error. A cancelled job stays viewable, so on success we do NOT
// navigate; the ['job', id] invalidation flips the status pill on refetch.
export function JobActions({ job }: { job: JobDetail }) {
  const { cancel } = useJobActions(job.id)
  const [confirm, setConfirm] = useState<Pending>(null)

  // Hide the buttons only for states the server treats as terminal for cancel
  // (cancelled/done). `failed` is NOT terminal server-side, so it stays
  // cancellable and keeps its buttons.
  const terminal = job.status === 'cancelled' || job.status === 'done'

  const actionError = cancel.error as Error | null

  function openConfirm(which: Exclude<Pending, null>) {
    cancel.reset()
    setConfirm(which)
  }

  function runConfirmed() {
    if (confirm === 'cancel') cancel.mutate(false)
    else if (confirm === 'force') cancel.mutate(true)
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
  }

  return (
    <div className="flex flex-col gap-2">
      {!terminal && (
        <div className="flex items-center gap-2">
          <PillButton variant="ghost" disabled={cancel.isPending} onClick={() => openConfirm('cancel')}>
            Cancel
          </PillButton>
          <PillButton variant="danger" disabled={cancel.isPending} onClick={() => openConfirm('force')}>
            Force cancel
          </PillButton>
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
