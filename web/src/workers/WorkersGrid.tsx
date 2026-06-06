import { Link } from 'react-router-dom'
import { StatusDot } from './StatusDot'
import { formatRelativeTime, labelChips, livenessView, specLine } from './liveness'
import type { Worker } from './api'

export function WorkersGrid({ workers }: { workers: Worker[] }) {
  return (
    <div className="grid grid-cols-[repeat(auto-fill,minmax(280px,1fr))] gap-3">
      {workers.map((w) => (
        <Link
          key={w.id}
          to={`/workers/${w.id}`}
          className={`block rounded-card border border-border bg-white/5 p-4 backdrop-blur transition hover:border-accent/50 ${livenessView(w.status).dimClass}`}
        >
          <div className="mb-2 flex items-baseline justify-between">
            <span className="font-mono text-[13px] text-fg">{w.name}</span>
            <StatusDot status={w.status} />
          </div>
          <div className="mb-2 font-mono text-[11px] text-fg-mute">{w.max_slots} slots</div>
          {labelChips(w.labels).length > 0 && (
            <div className="mb-2 flex flex-wrap gap-1">
              {labelChips(w.labels).map((c) => (
                <span
                  key={c}
                  className="rounded-full border border-accent/40 bg-accent/10 px-2 py-0.5 font-mono text-[9.5px] text-accent"
                >
                  {c}
                </span>
              ))}
            </div>
          )}
          <div className="mt-2 flex justify-between border-t border-border pt-2 font-mono text-[10px] text-fg-mute">
            <span>{specLine(w)}</span>
            <span>{w.last_seen_at ? formatRelativeTime(w.last_seen_at) : '-'}</span>
          </div>
        </Link>
      ))}
    </div>
  )
}
