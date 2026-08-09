import { apiFetch, apiStream } from '../lib/api'

export type JobStatus = 'pending' | 'running' | 'done' | 'failed' | 'cancelled'

export interface Job {
  id: string
  name: string
  priority: string
  status: JobStatus
  submitted_by_email?: string
  labels: Record<string, string> | null
  created_at: string
  updated_at: string
  total_tasks?: number
  done_tasks?: number
  started_at?: string
  finished_at?: string
  scheduled_job_id?: string
  scheduled_job_name?: string
}

export interface JobStats {
  running: number
  queued: number
  done_24h: number
  failed_24h: number
}

export interface JobsPage {
  items: Job[]
  next_cursor: string
  total: number
}

export type JobSort =
  | '-created_at'
  | 'created_at'
  | 'name'
  | '-name'
  | 'priority'
  | '-priority'
  | 'status'
  | '-status'
  | 'updated_at'
  | '-updated_at'

// First page is 50 (server default), passed explicitly. When a status filter is
// active the server rejects ?sort= combined with ?status=, so sort is omitted in
// that case; the unfiltered branch sends sort.
export function listJobs(sort: JobSort, status = '', cursor = ''): Promise<JobsPage> {
  const q = new URLSearchParams({ limit: '50' })
  if (status) q.set('status', status)
  else q.set('sort', sort)
  if (cursor) q.set('cursor', cursor)
  return apiFetch<JobsPage>(`/jobs?${q}`)
}

// Fleet-wide KPI counts for the summary strip.
export function getJobStats(): Promise<JobStats> {
  return apiFetch<JobStats>('/jobs/stats')
}

// Task-status vocabulary (migration 000019). Distinct from JobStatus: tasks add
// `dispatched` and `timed_out` and never use `cancelled` (a cancelled job's
// tasks are marked `failed` server-side).
export type TaskStatus = 'pending' | 'dispatched' | 'running' | 'done' | 'failed' | 'timed_out'

// One task as returned inside GET /v1/jobs/:id. `depends_on` is task NAMES, not
// IDs, resolved server-side; omitted when the task has no dependencies.
export interface TaskDetail {
  id: string
  name: string
  status: TaskStatus
  // commands/env/requires come from opaque JSON columns. The server returns `null`
  // (not `{}`/`[]`) for a task that omits env/requires (json.Marshal of a nil map is
  // `null`), so these are nullable on the wire; consumers must guard.
  commands: string[][] | null
  env: Record<string, string> | null
  requires: Record<string, string> | null
  timeout_seconds: number | null
  retries: number
  retry_count: number
  depends_on?: string[]
  worker_id?: string
}

// GET /v1/jobs/:id. NOTE: the detail endpoint does NOT return total_tasks,
// done_tasks, started_at, or finished_at (those are list-only). Derive progress
// from `tasks`.
export interface JobDetail {
  id: string
  name: string
  priority: string
  status: JobStatus
  submitted_by: string
  submitted_by_email?: string
  labels: Record<string, string> | null
  tasks: TaskDetail[]
  created_at: string
  updated_at: string
}

export interface LogEntry {
  seq: number
  stream: 'stdout' | 'stderr'
  content: string
  created_at: string
}

export interface TaskLogPage {
  items: LogEntry[]
  next_seq: number
  total: number
}

// Fetches one job with its full task list. Throws ApiError(404) if absent.
export function getJob(id: string): Promise<JobDetail> {
  return apiFetch<JobDetail>(`/jobs/${id}`)
}

/**
 * Backfill page size. The server caps ?limit= at 200 (internal/api/tasks.go:84),
 * and 200 is used so a full history costs the fewest requests.
 */
export const BACKFILL_PAGE_SIZE = 200

/**
 * The SSE task_log payload. seq/stream/content/created_at are field-identical to
 * LogEntry above, which is a backend guarantee (README.md:1330-1332), so one
 * client-side type covers both the live and the polled surface.
 */
export interface TaskLogEvent extends LogEntry {
  task_id: string
  job_id: string
}

/**
 * One page of a task's log history, forward-only from sinceSeq. next_seq is 0
 * when drained (internal/api/tasks.go:128-130). Always sends an explicit limit so
 * the caller is never silently truncated to the server default of 50.
 */
export function getTaskLogs(
  taskId: string,
  sinceSeq = 0,
  limit = BACKFILL_PAGE_SIZE,
): Promise<TaskLogPage> {
  const q = new URLSearchParams({ limit: String(limit) })
  if (sinceSeq > 0) q.set('since_seq', String(sinceSeq))
  return apiFetch<TaskLogPage>(`/tasks/${taskId}/logs?${q}`)
}

export interface TaskLogStreamOptions {
  signal: AbortSignal
  onLine: (entry: TaskLogEvent) => void
  onDropped: () => void
  onOpen?: () => void
  fetchImpl?: typeof fetch
}

/**
 * Subscribes to one task's live log lines. Resolves when the stream ENDS - which
 * is abnormal, because the server never ends a stream on its own
 * (README.md:1310-1313), so the caller treats a resolve as a failure for backoff
 * purposes rather than as an end of data. Rejects on a non-ok response, a
 * transport error, or an abort.
 *
 * Only ?task_id= is sent. Adding ?job_id= would put status frames on the same
 * 64-slot buffer, so a log burst could drop-close the connection including its
 * status frames (README.md:1352-1355); job/task status comes from useJob's poll
 * instead (spec Decision 2).
 */
export function streamTaskLog(taskId: string, opts: TaskLogStreamOptions): Promise<void> {
  return apiStream(`/events?task_id=${encodeURIComponent(taskId)}`, {
    signal: opts.signal,
    fetchImpl: opts.fetchImpl,
    onOpen: opts.onOpen,
    onEvent: (frame) => {
      if (frame.event === 'task_log') {
        try {
          opts.onLine(JSON.parse(frame.data) as TaskLogEvent)
        } catch {
          // A malformed frame is dropped silently. Never log frame.data: it is
          // raw subprocess output and can carry secrets.
        }
        return
      }
      if (frame.event === 'dropped') {
        opts.onDropped()
      }
      // Anything else is ignored: a ?task_id=-only subscription receives no status
      // frames (README.md:1312-1313), and unknown event types are additive.
    },
  })
}

// Cancels a job. force=true asks agents to force-kill running tasks; the DB
// effect (mark all non-terminal tasks and the job cancelled) is identical either
// way. Server 409s a job already `cancelled`/`done`, 404s a non-owner non-admin.
// The server returns the updated job body, but the caller invalidates rather than
// writing it into the cache, so the typed result is unused.
export function cancelJob(id: string, force: boolean): Promise<JobDetail> {
  return apiFetch<JobDetail>(`/jobs/${id}${force ? '?force=true' : ''}`, { method: 'DELETE' })
}

// Creates a job from a raw parsed job-spec object. The client keeps the spec
// type permissive (unknown) and posts it verbatim; the server (ValidateJobSpec)
// is the validator of record, so new TaskSpec fields need no client change. The
// 201 body is a jobResponse; JobDetail is the closest existing type and carries
// the `id` the caller navigates to.
export function createJob(spec: unknown): Promise<JobDetail> {
  return apiFetch<JobDetail>('/jobs', { method: 'POST', json: spec })
}
