import { Button } from '../components/Button'
import type { LogEntry } from './api'

// Static historical log renderer. Fetch-once semantics live in the hook; this is
// a pure view over the resolved items plus loading/error/empty states. NO SSE, no
// follow toggle, no auto-scroll-to-tail: live tailing is backend-blocked
// (docs/backlog/feature-2026-06-26-sse-task-log-publishing.md enabler +
// docs/backlog/feature-2026-06-26-task-log-view-sse-tailing.md web consumer). We
// signal that honestly with a STATIC/HISTORY marker, not a fake LIVE badge.
export function LogTab({
  items,
  isLoading,
  isError,
  onRetry,
}: {
  items: LogEntry[]
  isLoading: boolean
  isError: boolean
  onRetry: () => void
}) {
  if (isLoading) {
    return <div className="p-4 text-[12px] text-fg-mute">Loading logs...</div>
  }
  if (isError) {
    return (
      <div className="flex flex-col items-start gap-2 p-4">
        <div className="text-[12px] text-err">Failed to load logs.</div>
        <Button className="w-auto px-4" onClick={onRetry}>Retry</Button>
      </div>
    )
  }
  if (items.length === 0) {
    return <div className="p-4 text-[12px] text-fg-mute">No log output.</div>
  }
  return (
    <div className="flex flex-col">
      <div className="flex items-center justify-between border-b border-border px-3 py-2 font-mono text-[10px] tracking-[0.14em] text-fg-mute">
        <span className="text-fg-dim">STATIC · HISTORY</span>
        <span>live tailing pending</span>
      </div>
      <div className="flex flex-col gap-0.5 bg-black/25 p-3 font-mono text-[11px]">
        {items.map((l) => (
          <div key={l.seq} className={l.stream === 'stderr' ? 'text-err' : 'text-fg'}>
            {l.content}
          </div>
        ))}
      </div>
    </div>
  )
}
