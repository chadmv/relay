import { formatRelativeTime } from './liveness'
import { useWorkerWorkspaces } from './useWorkerWorkspaces'

const COLS = 'grid grid-cols-[120px_90px_1fr_120px_90px]'

// Admin-only, read-only source workspaces table. Mounted by WorkerDetailPage
// only when the current user is an admin.
export function WorkspacesPanel({ workerId }: { workerId: string }) {
  const { data, isLoading } = useWorkerWorkspaces(workerId)
  const rows = data ?? []
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
          </div>
        ))}
      </div>
    </div>
  )
}
