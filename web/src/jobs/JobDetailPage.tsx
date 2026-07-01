import { useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { ApiError } from '../lib/api'
import { Button } from '../components/Button'
import { statusColor, progressPct } from './status'
import { TasksTable } from './TasksTable'
import { TaskDag } from './TaskDag'
import { SpecTab } from './SpecTab'
import { LogTab } from './LogTab'
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

  const logs = useTaskLogs(selectedTaskId, selectedTaskId !== '' && tab === 'log')

  if (isLoading && !job) {
    return <div className="h-40 rounded-card border border-border bg-white/5" />
  }

  if (error && !job) {
    const notFound = error instanceof ApiError && error.status === 404
    return (
      <div className="mx-auto mt-10 max-w-md rounded-card border border-border bg-white/5 p-6 text-center">
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
      </div>
    )
  }

  if (!job) return null

  const c = statusColor(job.status)
  const done = tasks.filter((t) => t.status === 'done').length
  const total = tasks.length
  const active = tasks.filter((t) => t.status === 'running' || t.status === 'dispatched').length
  const pct = progressPct(done, total)
  const chips = Object.entries(job.labels ?? {}).map(([k, v]) => (v ? `${k}=${v}` : k))

  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-col gap-1">
        <Link to="/jobs" className="font-mono text-[11px] text-fg-mute hover:text-fg">&larr; Jobs</Link>
        <div className="flex items-center gap-3">
          <h1 className="text-[28px] font-normal tracking-tight">{job.name}</h1>
          <span className={`flex items-center gap-2 font-mono text-[12px] ${c.text}`}>
            <span className={`h-1.5 w-1.5 rounded-full ${c.dot}`} />
            {job.status}
          </span>
          {/* Reserved actions slot: cancel/retry deferred to a later slice. Intentionally empty. */}
          <div data-testid="job-actions" className="ml-auto flex items-center gap-2" />
        </div>
        <div className="font-mono text-[11px] text-fg-mute">
          id {job.id.slice(0, 8)} · submitted by {job.submitted_by_email ?? '-'} · priority {job.priority}
        </div>
        {chips.length > 0 && (
          <div className="mt-1 flex flex-wrap gap-1">
            {chips.map((ch) => (
              <span key={ch} className="rounded-full border border-accent/40 bg-accent/10 px-2 py-0.5 font-mono text-[10px] text-accent">
                {ch}
              </span>
            ))}
          </div>
        )}
      </div>

      <div className="flex flex-col gap-5 lg:flex-row">
        <div className="flex flex-col gap-4 lg:w-[55%]">
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
          <div className="rounded-b-card border border-t-0 border-border bg-white/5">
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
          </div>
        </div>
      </div>
    </div>
  )
}
