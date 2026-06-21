import type { Job } from './api'
import { statusColor, progressPct, formatDuration, formatStarted } from './status'

const COLS = 'grid grid-cols-[90px_1fr_120px_150px_120px_70px_150px]'

export function JobsTable({ jobs }: { jobs: Job[] }) {
  if (jobs.length === 0) {
    return (
      <div className="mx-auto mt-10 max-w-md rounded-card border border-border bg-white/5 p-6 text-center text-[13px] text-fg-mute">
        No jobs yet.
      </div>
    )
  }
  return (
    <div className="rounded-card border border-border bg-white/5 backdrop-blur">
      <div className={`${COLS} border-b border-border px-4 py-3 font-mono text-[10px] tracking-wider text-fg-mute`}>
        <span>ID</span>
        <span>NAME</span>
        <span>STATUS</span>
        <span>PROGRESS</span>
        <span>STARTED</span>
        <span>DUR</span>
        <span>OWNER</span>
      </div>
      {jobs.map((j) => {
        const c = statusColor(j.status)
        const pct = progressPct(j.done_tasks, j.total_tasks)
        return (
          <div
            key={j.id}
            className={`${COLS} items-center border-b border-border/40 px-4 py-2 font-mono text-[11.5px]`}
          >
            <span className="text-fg-mute">{j.id.slice(0, 6)}</span>
            <span className="flex min-w-0 items-center gap-2">
              <span className="truncate font-sans text-[13px] text-fg">{j.name}</span>
              {j.scheduled_job_name && (
                <span className="flex-none rounded-full border border-accent-b/40 bg-accent-b/10 px-1.5 py-0.5 text-[9.5px] text-accent-b">
                  ⟳ {j.scheduled_job_name}
                </span>
              )}
            </span>
            <span className={`flex items-center gap-2 ${c.text}`}>
              <span className={`h-1.5 w-1.5 rounded-full ${c.dot}`} />
              {j.status}
            </span>
            <span className="grid grid-cols-[1fr_36px] items-center gap-2 pr-4">
              <span className="relative h-1 overflow-hidden rounded bg-white/10">
                <span
                  className={`absolute inset-y-0 left-0 rounded ${
                    j.status === 'done' ? 'bg-ok' : j.status === 'failed' ? 'bg-err' : 'bg-accent'
                  }`}
                  style={{ width: `${pct}%` }}
                />
              </span>
              <span className="text-right text-fg">{pct}%</span>
            </span>
            <span className="text-fg-mute">{formatStarted(j.started_at)}</span>
            <span className="text-fg-mute">{formatDuration(j.started_at, j.finished_at)}</span>
            <span className="truncate text-[11px] text-fg-mute">{j.submitted_by_email ?? '-'}</span>
          </div>
        )
      })}
    </div>
  )
}
