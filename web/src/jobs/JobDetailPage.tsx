import { useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { ApiError } from '../lib/api'
import { Button } from '../components/Button'
import { Chip, GlassPanel } from '../components/holo'
import { useAuth } from '../auth/AuthProvider'
import { statusColor, progressPct } from './status'
import { TasksTable } from './TasksTable'
import { TaskDag } from './TaskDag'
import { SpecTab } from './SpecTab'
import { LogTab } from './LogTab'
import { JobActions } from './JobActions'
import { useJob } from './useJob'
import { useTaskLogs } from './useTaskLogs'
import type { TaskDetail } from './api'

type Tab = 'spec' | 'log'

// Picks the most useful default task: the first running/failed one if present,
// else the first task. Returns '' for an empty job.
function defaultTaskId(tasks: TaskDetail[]): string {
  const active = tasks.find((t) => t.status === 'running' || t.status === 'failed' || t.status === 'timed_out')
  return active?.id ?? tasks[0]?.id ?? ''
}

export function JobDetailPage() {
  const { id = '' } = useParams()
  const { user } = useAuth()
  const { data: job, error, isLoading, refetch } = useJob(id)
  const [tab, setTab] = useState<Tab>('spec')
  const [pickedTaskId, setPickedTaskId] = useState<string>('')

  const tasks = job?.tasks ?? []

  // Effective selection: an explicit pick if it still matches a task, else the
  // default. This falls back automatically when a poll changes the task list.
  const selectedTaskId = useMemo(() => {
    if (pickedTaskId && tasks.some((t) => t.id === pickedTaskId)) return pickedTaskId
    return defaultTaskId(tasks)
  }, [pickedTaskId, tasks])

  const selectedTask = tasks.find((t) => t.id === selectedTaskId)

  // Log query is decoupled from ['job', ...] and gated to the Log tab, so a job
  // poll never disturbs it and we never fetch logs for an unopened tab.
  const logs = useTaskLogs(selectedTaskId, selectedTaskId !== '' && tab === 'log')

  if (isLoading && !job) {
    return <GlassPanel className="h-40" />
  }

  if (error && !job) {
    const notFound = error instanceof ApiError && error.status === 404
    return (
      <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center">
        {notFound ? (
          <div className="text-[13px] text-fg-mute">Job not found.</div>
        ) : (
          <>
            <div className="mb-3 text-[13px] text-err">{(error as Error).message}</div>
            <Button className="w-auto px-4" onClick={() => refetch()}>Retry</Button>
          </>
        )}
        <div className="mt-4">
          <Link to="/jobs" className="font-mono text-[11px] text-accent">&larr; Jobs</Link>
        </div>
      </GlassPanel>
    )
  }

  if (!job) return null

  const canManage = Boolean(user && (user.is_admin || job.submitted_by === user.id))

  const c = statusColor(job.status)
  // Progress is DERIVED from tasks[]: the detail endpoint returns no total_tasks/
  // done_tasks/started_at/finished_at (those are list-only enrichment). The hi-fi
  // header also shows STARTED/elapsed/ETA/duration - all omitted (no field on the
  // wire): docs/backlog/feature-2026-07-01-job-detail-timing-enrichment.md.
  const done = tasks.filter((t) => t.status === 'done').length
  const total = tasks.length
  const active = tasks.filter((t) => t.status === 'running' || t.status === 'dispatched').length
  const pct = progressPct(done, total)
  const queued = tasks.filter((t) => t.status === 'pending').length
  const chips = Object.entries(job.labels ?? {}).map(([k, v]) => (v ? `${k}=${v}` : k))

  return (
    <div className="flex flex-col gap-5">
      {/* Breadcrumb + header row: back link, id, name, inline status; the reserved
          JobActions slot (ml-auto). No Retry/Abort header pill - there is no per-job
          retry endpoint and "Abort" is just cancel; the real Cancel/Force cancel
          live in JobActions. */}
      <div className="flex flex-col gap-1">
        <div className="flex items-center gap-2.5">
          <Link to="/jobs" className="font-mono text-[11px] text-fg-mute hover:text-fg">&larr; Jobs</Link>
          <span className="text-fg-dim">/</span>
          <span className="font-mono text-[12px] text-accent">{job.id.slice(0, 8)}</span>
          <span className="text-fg-dim">/</span>
          <h1 className="text-[28px] font-normal tracking-tight">{job.name}</h1>
          {/* Inline status uses the JobStatus map (status.ts), NOT the worker
              StatusDot (WorkerStatus vocabulary). */}
          <span className={`flex items-center gap-2 font-mono text-[12px] ${c.text}`}>
            <span className={`h-1.5 w-1.5 rounded-full ${c.dot}`} />
            {job.status}
          </span>
          <div data-testid="job-actions" className="ml-auto flex items-center gap-2">
            {canManage && <JobActions job={job} />}
          </div>
        </div>
        <div className="font-mono text-[11px] text-fg-mute">
          id {job.id.slice(0, 8)} · submitted by {job.submitted_by_email ?? '-'} · priority {job.priority}
        </div>
        {chips.length > 0 && (
          <div className="mt-1 flex flex-wrap gap-1">
            {chips.map((ch) => (
              <Chip key={ch} tone="accent">{ch}</Chip>
            ))}
          </div>
        )}
      </div>

      {/* Body: fixed 55/45 split. The accessible drag-resizer is a filed follow-up:
          docs/backlog/idea-2026-07-01-job-detail-resizable-split.md. */}
      <div className="flex flex-col gap-5 lg:flex-row">
        <div className="flex flex-col gap-4 lg:w-[55%]">
          {/* Derived progress strip: done/total + active, status-toned bar. Kept as
              an inline per-status bar (ProgressBar has only accent/muted tones). */}
          <div className="flex flex-col gap-2">
            <div className="flex items-baseline justify-between font-mono text-[11px] text-fg-mute">
              <span>{done} / {total} tasks done</span>
              <span>{active} active</span>
            </div>
            <span className="relative h-1.5 overflow-hidden rounded bg-white/10">
              <span
                className={`absolute inset-y-0 left-0 rounded ${
                  job.status === 'done' ? 'bg-ok' : job.status === 'failed' ? 'bg-err' : 'bg-accent'
                }`}
                style={{ width: `${pct}%` }}
              />
            </span>
          </div>

          {/* Pipeline panel header carries the real derived active/queued counts
              (replaces the hi-fi "STAGE 4 / 8" + "CLICK TO STREAM" mock strings;
              click-to-stream implies live logs we cannot deliver). */}
          <div className="flex items-center justify-between px-1 font-mono text-[10px] tracking-[0.14em] text-fg-mute">
            <span>PIPELINE</span>
            <span>{active} ACTIVE · {queued} QUEUED</span>
          </div>
          <TaskDag tasks={tasks} />
          <TasksTable tasks={tasks} selectedTaskId={selectedTaskId} onSelect={setPickedTaskId} />
        </div>

        <div className="flex flex-col lg:w-[45%]">
          <div role="tablist" aria-label="Task detail" className="flex gap-1 border-b border-border">
            <button
              type="button"
              role="tab"
              aria-selected={tab === 'spec'}
              onClick={() => setTab('spec')}
              className={`px-3 py-2 text-[12px] ${tab === 'spec' ? 'border-b-2 border-accent text-fg' : 'text-fg-mute'}`}
            >
              Spec
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={tab === 'log'}
              onClick={() => setTab('log')}
              className={`px-3 py-2 text-[12px] ${tab === 'log' ? 'border-b-2 border-accent text-fg' : 'text-fg-mute'}`}
            >
              Log
            </button>
          </div>
          <GlassPanel className="rounded-t-none border-t-0">
            {tab === 'spec' ? (
              <SpecTab task={selectedTask} />
            ) : (
              <LogTab
                items={logs.data?.items ?? []}
                isLoading={logs.isLoading}
                isError={logs.isError}
                onRetry={() => logs.refetch()}
              />
            )}
          </GlassPanel>
        </div>
      </div>
    </div>
  )
}
