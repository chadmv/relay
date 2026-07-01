import { Link } from 'react-router-dom'
import { Chip, GlassPanel, StatusDot } from '../components/holo'
import { formatRelativeTime, labelChips, livenessView, specLine } from './liveness'
import type { Worker } from './api'

export function WorkersGrid({ workers }: { workers: Worker[] }) {
  return (
    <div className="grid grid-cols-[repeat(auto-fill,minmax(280px,1fr))] gap-3">
      {workers.map((w) => (
        <GlassPanel
          as={Link}
          key={w.id}
          to={`/workers/${w.id}`}
          className={`block p-4 transition hover:border-accent/50 ${livenessView(w.status).dimClass}`}
        >
          <div className="mb-2 flex items-baseline justify-between">
            <span className="font-mono text-[13px] text-fg">{w.name}</span>
            <StatusDot status={w.status} />
          </div>
          <div className="mb-2 font-mono text-[11px] text-fg-mute">{w.max_slots} slots</div>
          {labelChips(w.labels).length > 0 && (
            <div className="mb-2 flex flex-wrap gap-1">
              {labelChips(w.labels).map((c) => (
                <Chip key={c}>{c}</Chip>
              ))}
            </div>
          )}
          <div className="mt-2 flex justify-between border-t border-border pt-2 font-mono text-[10px] text-fg-mute">
            <span>{specLine(w)}</span>
            <span>{w.last_seen_at ? formatRelativeTime(w.last_seen_at) : '-'}</span>
          </div>
        </GlassPanel>
      ))}
    </div>
  )
}
