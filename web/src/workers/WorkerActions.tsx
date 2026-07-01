import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { WorkerEditForm } from './WorkerEditForm'
import { useWorkerActions } from './useWorkerActions'
import type { Worker, WorkerPatch } from './api'

type Pending = null | 'disable' | 'drain' | 'revoke'

// Admin-only action bar for the worker detail page. Owns the edit-form toggle,
// the confirm dialog for destructive/disruptive actions, and the inline error.
export function WorkerActions({ worker }: { worker: Worker }) {
  const navigate = useNavigate()
  const { update, disable, enable, revoke } = useWorkerActions(worker.id)
  const [editing, setEditing] = useState(false)
  const [confirm, setConfirm] = useState<Pending>(null)

  const busy =
    update.isPending || disable.isPending || enable.isPending || revoke.isPending
  const isDisabled = Boolean(worker.disabled_at)
  const actionError = (update.error ?? disable.error ?? enable.error ?? revoke.error) as
    | Error
    | null

  function onSave(patch: WorkerPatch) {
    update.mutate(patch, { onSuccess: () => setEditing(false) })
  }

  function runConfirmed() {
    if (confirm === 'disable') disable.mutate(false)
    else if (confirm === 'drain') disable.mutate(true)
    else if (confirm === 'revoke') revoke.mutate(undefined, { onSuccess: () => navigate('/workers') })
    setConfirm(null)
  }

  const confirmCopy: Record<Exclude<Pending, null>, { title: string; body: string; label: string; destructive?: boolean }> = {
    disable: {
      title: `Disable ${worker.name}?`,
      body: 'It will stop receiving new tasks. In-flight tasks keep running.',
      label: 'Disable',
    },
    drain: {
      title: `Drain ${worker.name}?`,
      body: 'It stops receiving new tasks and its in-flight tasks are requeued to other workers and cancelled here.',
      label: 'Drain',
    },
    revoke: {
      title: `Revoke ${worker.name}'s agent token?`,
      body: 'This decommissions the worker. It disappears from the fleet and must re-enroll to return.',
      label: 'Revoke',
      destructive: true,
    },
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap gap-2">
        <button
          type="button"
          onClick={() => setEditing((v) => !v)}
          className="rounded-md border border-border bg-white/5 px-3 py-1.5 text-[12px] text-fg"
        >
          Edit
        </button>
        {isDisabled ? (
          <button
            type="button"
            disabled={busy}
            onClick={() => {
              disable.reset()
              enable.mutate()
            }}
            className="rounded-md border border-accent/50 bg-accent/15 px-3 py-1.5 text-[12px] text-fg disabled:opacity-40"
          >
            Enable
          </button>
        ) : (
          <>
            <button
              type="button"
              disabled={busy}
              onClick={() => setConfirm('disable')}
              className="rounded-md border border-border bg-white/5 px-3 py-1.5 text-[12px] text-fg-mute disabled:opacity-40"
            >
              Disable
            </button>
            <button
              type="button"
              disabled={busy}
              onClick={() => setConfirm('drain')}
              className="rounded-md border border-border bg-white/5 px-3 py-1.5 text-[12px] text-fg-mute disabled:opacity-40"
            >
              Drain
            </button>
          </>
        )}
        <button
          type="button"
          disabled={busy}
          onClick={() => setConfirm('revoke')}
          className="rounded-md border border-err/50 bg-err/10 px-3 py-1.5 text-[12px] text-err disabled:opacity-40"
        >
          Revoke
        </button>
      </div>

      {editing && (
        <WorkerEditForm
          worker={worker}
          pending={update.isPending}
          onSubmit={onSave}
          onCancel={() => setEditing(false)}
        />
      )}

      {actionError ? (
        <div className="rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err">
          {actionError.message}
        </div>
      ) : null}

      {disable.data && disable.data.requeued_tasks > 0 ? (
        <div className="rounded-card border border-accent/40 bg-accent/10 px-4 py-2 text-[12px] text-accent">
          Requeued {disable.data.requeued_tasks} task(s).
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
