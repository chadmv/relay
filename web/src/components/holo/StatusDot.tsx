import { livenessView } from '../../workers/liveness'
import type { WorkerStatus } from '../../workers/api'

// The dot + mono status label. Moved here from web/src/workers/ so non-worker
// pages can use it. Keeps the livenessView-based API (worker statuses are the only
// consumer today).
export function StatusDot({ status }: { status: WorkerStatus }) {
  const v = livenessView(status)
  return (
    <span className={`inline-flex items-center gap-1.5 font-mono text-[10px] tracking-wider ${v.textClass}`}>
      <span className={`h-1.5 w-1.5 rounded-full ${v.dotClass}`} />
      {v.label}
    </span>
  )
}
