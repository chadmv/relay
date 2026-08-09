import { Link, useParams } from 'react-router-dom'
import { GlassPanel } from '../components/holo'
import { LogView } from './LogView'
import { useJob } from './useJob'
import { useTaskLogStream } from './useTaskLogStream'
import { isTerminalTask, taskStatusColor } from './taskStatus'

// Full-screen single-task log. The route is keyed by task UUID, not by an index:
// there is no task ordinal column, every task of a job shares one created_at
// (internal/api/jobs.go:202-209 inserts them in one transaction where NOW() is
// constant) and ORDER BY created_at has no tiebreaker, so a bookmarked
// /jobs/:id/tasks/3 would drift to a different task mid-job.
//
// It reuses useJob for the header (which also supplies the terminal signal) plus
// useTaskLogStream and LogView unchanged, so there is exactly one hook instance
// on this page. The hi-fi's Download button is deliberately omitted (spec,
// Omissions): it needs a full history the page cap does not fetch, and it is the
// affordance most likely to move secret-bearing output onto disk.
export function TaskLogPage() {
  const { id = '', taskId = '' } = useParams()
  const { data: job, isLoading } = useJob(id)
  const task = job?.tasks.find((t) => t.id === taskId)

  // enabled stays false until the job's task list confirms the task exists, so a
  // bad URL never opens a connection.
  const stream = useTaskLogStream(taskId, {
    live: task !== undefined && !isTerminalTask(task.status),
    enabled: task !== undefined,
  })

  if (isLoading && !job) return <GlassPanel className="h-40" />

  if (!task) {
    return (
      <GlassPanel className="mx-auto mt-10 max-w-md p-6 text-center">
        <div className="text-[13px] text-fg-mute">Task not found in this job.</div>
        <div className="mt-4">
          <Link to={`/jobs/${id}`} className="font-mono text-[11px] text-accent">
            &larr; Job detail
          </Link>
        </div>
      </GlassPanel>
    )
  }

  const c = taskStatusColor(task.status)

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2.5">
        <Link to={`/jobs/${id}`} className="font-mono text-[11px] text-fg-mute hover:text-fg">
          &larr; Job detail
        </Link>
        <span className="text-fg-dim">/</span>
        <span className="font-mono text-[12px] text-fg-mute">{id.slice(0, 8)}</span>
        <span className="text-fg-dim">/</span>
        <span className="font-mono text-[12px] text-accent">{taskId.slice(0, 8)}</span>
        <h1 className="text-[16px] font-normal tracking-tight">{task.name}</h1>
        <span className={`flex items-center gap-2 font-mono text-[12px] ${c.text}`}>
          <span className={`h-1.5 w-1.5 rounded-full ${c.dot}`} />
          {task.status}
        </span>
        <span className="font-mono text-[11px] text-fg-mute">
          worker {task.worker_id ? task.worker_id.slice(0, 6) : '-'} · retry {task.retry_count}/{task.retries}
        </span>
      </div>

      <GlassPanel className="flex min-h-0 flex-1 flex-col">
        <LogView
          stream={stream}
          endpointCaption={`/v1/events?task_id=${taskId} · single-task stream`}
          bodyClassName="min-h-0 flex-1 overflow-auto"
        />
      </GlassPanel>
    </div>
  )
}
