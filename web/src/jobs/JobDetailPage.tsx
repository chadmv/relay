import { useMemo, useRef, useState } from 'react'
import type { CSSProperties, KeyboardEvent } from 'react'
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
import { useTaskLogStream } from './useTaskLogStream'
import { isTerminalTask } from './taskStatus'
import { SPLIT_MAX, SPLIT_MIN, SPLIT_STEP } from './splitWidth'
import { useSplitWidth } from './useSplitWidth'
import type { TaskDetail } from './api'

type Tab = 'spec' | 'log'

// Picks the most useful default task: the first one actively syncing or
// running, or already failed, if present, else the first task. preparing is
// included because it is the one currently streaming logs the operator wants
// to see, not because it counts as an error state like failed/timed_out.
// Returns '' for an empty job.
function defaultTaskId(tasks: TaskDetail[]): string {
  const active = tasks.find(
    (t) => t.status === 'running' || t.status === 'preparing' || t.status === 'failed' || t.status === 'timed_out',
  )
  return active?.id ?? tasks[0]?.id ?? ''
}

export function JobDetailPage() {
  const { id = '' } = useParams()
  const { user } = useAuth()
  const { data: job, error, isLoading, refetch } = useJob(id)
  const [tab, setTab] = useState<Tab>('spec')
  const [pickedTaskId, setPickedTaskId] = useState<string>('')
  const splitRef = useRef<HTMLDivElement>(null)
  const split = useSplitWidth(splitRef)

  const tasks = job?.tasks ?? []

  // Effective selection: an explicit pick if it still matches a task, else the
  // default. This falls back automatically when a poll changes the task list.
  const selectedTaskId = useMemo(() => {
    if (pickedTaskId && tasks.some((t) => t.id === pickedTaskId)) return pickedTaskId
    return defaultTaskId(tasks)
  }, [pickedTaskId, tasks])

  const selectedTask = tasks.find((t) => t.id === selectedTaskId)

  // Log state lives in the hook, not the query cache (spec Decision 3), so a job
  // poll can never disturb it and no log line ever enters TanStack. `live` comes
  // from useJob's poll: a ?task_id= subscription has no terminal signal of its
  // own (README.md:1310-1313), and the selected task's status reaching a terminal
  // value within one poll interval is what tells us to stop tailing. A terminal
  // task therefore opens no connection at all, and leaving the Log tab flips
  // `enabled` false, which tears the connection down.
  const logStream = useTaskLogStream(selectedTaskId, {
    live: !isTerminalTask(selectedTask?.status),
    enabled: selectedTaskId !== '' && tab === 'log',
  })

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
  // preparing counts as active: a job whose tasks are all syncing must not read
  // as idle.
  const active = tasks.filter(
    (t) => t.status === 'running' || t.status === 'dispatched' || t.status === 'preparing',
  ).length
  const pct = progressPct(done, total)
  const queued = tasks.filter((t) => t.status === 'pending').length
  const chips = Object.entries(job.labels ?? {}).map(([k, v]) => (v ? `${k}=${v}` : k))

  // Left and Right only. Up and Down are deliberately unbound: this separator is
  // vertical and announces that, so a cross-axis binding would make the announced
  // orientation a lie. preventDefault runs only for a key this handles, so an
  // unhandled key still scrolls the page normally.
  function onSeparatorKeyDown(e: KeyboardEvent<HTMLDivElement>) {
    let next: number | null = null
    if (e.key === 'ArrowLeft') next = split.width - SPLIT_STEP
    else if (e.key === 'ArrowRight') next = split.width + SPLIT_STEP
    else if (e.key === 'Home') next = SPLIT_MIN
    else if (e.key === 'End') next = SPLIT_MAX
    if (next === null) return
    e.preventDefault()
    split.setWidth(next)
    // One press is one gesture, so it commits.
    split.persist()
  }

  return (
    <div className="flex flex-col gap-5">
      {/* Breadcrumb + header row: back link, id, name, inline status; the reserved
          JobActions slot (ml-auto). The hi-fi's "Abort" pill is just cancel, and its
          single "Retry" pill became two - Retry failed / Retry all - because
          POST /v1/jobs/{id}/retry requires ?task= and has no default. All four live
          in JobActions, which hides each pair for the statuses the server refuses. */}
      <div className="flex flex-col gap-1">
        {/* flex-wrap so the breadcrumb, the 28px title and the action bar stack
            instead of setting a floor under <main> - MAIN measured 458px at a 375px
            viewport without it. Matches TaskLogPage's breadcrumb row. */}
        <div className="flex flex-wrap items-center gap-2.5">
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

      {/* The split's percentage reaches CSS as a custom property on this
          container, consumed by breakpoint-prefixed arbitrary-value width
          utilities written as literals on the two panes below. Two consequences,
          both deliberate: the utilities are literals so Tailwind v4's static scan
          emits them, and the value applies only at and above the breakpoint,
          which an inline width could not express. Both panes are sized off the
          one property - the right one as the complement - so the pair sums to the
          same 100 percent basis the fixed pair did, and a reader with nothing
          persisted sees the same layout as before. */}
      <div
        ref={splitRef}
        className="flex flex-col gap-5 lg:flex-row"
        style={{ '--relay-split': `${split.width}%` } as CSSProperties}
      >
        <div className="flex flex-col gap-4 lg:w-[var(--relay-split)]">
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

        {/* Hidden below the breakpoint, where the panes stack and there is
            nothing to resize: a separator that resizes nothing is a dead control
            and a dead tab stop. */}
        <div
          role="separator"
          tabIndex={0}
          aria-orientation="vertical"
          aria-label="Resize the pipeline and task detail panes"
          aria-valuenow={split.width}
          aria-valuemin={SPLIT_MIN}
          aria-valuemax={SPLIT_MAX}
          aria-valuetext={`pipeline ${split.width}%, task detail ${100 - split.width}%`}
          title="Drag to resize"
          onPointerDown={split.onPointerDown}
          onKeyDown={onSeparatorKeyDown}
          className="relative hidden w-1.5 shrink-0 cursor-col-resize self-stretch focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-accent lg:block"
        >
          <span className="absolute left-1/2 top-1/2 h-9 w-0.5 -translate-x-1/2 -translate-y-1/2 rounded bg-accent/30" />
        </div>

        <div className="flex flex-col lg:w-[calc(100%_-_var(--relay-split))]">
          {/* The name carries the selected task, so a user moving to Spec or Log hears
              whose spec and log they are about to read. React escapes attribute
              values and an aria-label is not parsed as markup, so a hostile task
              name is a nuisance in an announcement, not an injection. */}
          <div
            role="tablist"
            aria-label={selectedTask ? `Task detail: ${selectedTask.name}` : 'Task detail'}
            className="flex gap-1 border-b border-border"
          >
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
              <LogTab jobId={job.id} taskId={selectedTaskId} stream={logStream} />
            )}
          </GlassPanel>
        </div>
      </div>
    </div>
  )
}
