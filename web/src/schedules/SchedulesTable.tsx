import type { Schedule } from './api'
import { formatRelativeTime, nextRunDisplay, shortId } from './format'

const COLS = 'grid grid-cols-[1.4fr_120px_110px_90px_1fr_1fr_110px_1.3fr_150px]'

export function SchedulesTable({
  schedules,
  pendingId,
  onRunNow,
  onToggleEnabled,
}: {
  schedules: Schedule[]
  pendingId: string | null
  onRunNow: (id: string) => void
  onToggleEnabled: (id: string, nextEnabled: boolean) => void
}) {
  return (
    <div className="rounded-card border border-border bg-white/5 backdrop-blur">
      <div className={`${COLS} border-b border-border px-4 py-3 font-mono text-[10px] tracking-wider text-fg-mute`}>
        <span>NAME</span>
        <span>CRON</span>
        <span>TZ</span>
        <span>OVERLAP</span>
        <span>NEXT RUN</span>
        <span>LAST RUN</span>
        <span>LAST JOB</span>
        <span>OWNER</span>
        <span className="text-right">ACTIONS</span>
      </div>
      {schedules.map((s) => {
        const pending = pendingId === s.id
        return (
          <div
            key={s.id}
            className={`${COLS} items-center border-b border-border/40 px-4 py-2 font-mono text-[11.5px] ${s.enabled ? '' : 'opacity-[0.55]'}`}
          >
            <span className="flex min-w-0 items-center gap-2">
              <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${s.enabled ? 'bg-ok' : 'bg-fg-dim'}`} />
              <span className="truncate font-sans text-[13px] text-fg">{s.name}</span>
            </span>
            <span className="text-fg">{s.cron_expr}</span>
            <span className="truncate text-[10.5px] text-fg-mute">{s.timezone}</span>
            <span>
              <span
                className={`rounded-full border border-border px-1.5 py-0.5 text-[9.5px] uppercase tracking-wider ${s.overlap_policy === 'allow' ? 'text-accent' : 'text-fg-mute'}`}
              >
                {s.overlap_policy}
              </span>
            </span>
            <span className={s.enabled ? 'text-fg' : 'text-fg-dim'}>
              {s.enabled ? <span className="text-accent">&#9658;</span> : null} {nextRunDisplay(s.next_run_at)}
            </span>
            <span className="text-fg-mute">{s.last_run_at ? formatRelativeTime(s.last_run_at) : '-'}</span>
            <span className="text-[10.5px] text-fg-mute">{shortId(s.last_job_id)}</span>
            <span className="truncate text-[10.5px] text-fg-mute">{s.owner_email}</span>
            <span className="flex justify-end gap-1.5">
              <button
                type="button"
                disabled={pending}
                onClick={() => onRunNow(s.id)}
                className="rounded-md border border-accent/50 bg-accent/15 px-2.5 py-1 text-[11px] text-fg disabled:opacity-40"
              >
                Run now
              </button>
              <button
                type="button"
                disabled={pending}
                onClick={() => onToggleEnabled(s.id, !s.enabled)}
                className="rounded-md border border-border bg-white/5 px-2.5 py-1 text-[11px] text-fg-mute disabled:opacity-40"
              >
                {s.enabled ? 'Disable' : 'Enable'}
              </button>
            </span>
          </div>
        )
      })}
    </div>
  )
}
