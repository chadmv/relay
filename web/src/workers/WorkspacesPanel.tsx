import { useState } from 'react'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { formatRelativeTime } from './liveness'
import { useWorkerActions } from './useWorkerActions'
import { useWorkerWorkspaces } from './useWorkerWorkspaces'

const COLS = 'grid grid-cols-[120px_90px_1fr_120px_90px_90px]'

// Admin-only source workspaces table with per-row evict. Mounted by
// WorkerDetailPage only when the current user is an admin, so no inner is_admin
// check is needed. Eviction is best-effort/async (202): the row does not vanish
// immediately; the 15s workspace poll reconciles once the agent confirms.
export function WorkspacesPanel({ workerId }: { workerId: string }) {
  const { data, isLoading } = useWorkerWorkspaces(workerId)
  const { evict } = useWorkerActions(workerId)
  const [confirmId, setConfirmId] = useState<string | null>(null)
  const rows = data ?? []

  function runEvict() {
    if (confirmId) evict.mutate(confirmId)
    setConfirmId(null)
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="font-mono text-[11px] tracking-widest text-fg-mute">SOURCE WORKSPACES</div>
      <div className="rounded-card border border-border bg-white/5">
        <div className={`${COLS} border-b border-border px-4 py-2 font-mono text-[10px] tracking-wider text-fg-mute`}>
          <span>SHORT ID</span>
          <span>TYPE</span>
          <span>SOURCE KEY</span>
          <span>BASELINE</span>
          <span>LAST USED</span>
          <span className="text-right">ACTIONS</span>
        </div>
        {!isLoading && rows.length === 0 && (
          <div className="px-4 py-3 text-[12px] text-fg-mute">No workspaces.</div>
        )}
        {rows.map((ws) => (
          <div
            key={ws.short_id}
            className={`${COLS} items-center border-b border-border/40 px-4 py-2 font-mono text-[11px]`}
          >
            <span className="text-fg">{ws.short_id}</span>
            <span className="text-fg-mute">{ws.source_type}</span>
            <span className="truncate text-fg-mute">{ws.source_key}</span>
            <span className="text-fg-mute">{ws.baseline_hash}</span>
            <span className="text-fg-mute">{formatRelativeTime(ws.last_used_at)}</span>
            <span className="flex justify-end">
              <button
                type="button"
                disabled={evict.isPending}
                onClick={() => setConfirmId(ws.short_id)}
                className="rounded-md border border-err/50 bg-err/10 px-2 py-0.5 text-[10px] text-err disabled:opacity-40"
              >
                Evict
              </button>
            </span>
          </div>
        ))}
      </div>

      {evict.error ? (
        <div className="rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err">
          {(evict.error as Error).message}
        </div>
      ) : null}

      {confirmId && (
        <ConfirmDialog
          title={`Evict workspace ${confirmId}?`}
          body="The agent removes it on next opportunity. A held workspace is refused."
          confirmLabel="Evict"
          onConfirm={runEvict}
          onCancel={() => setConfirmId(null)}
        />
      )}
    </div>
  )
}
