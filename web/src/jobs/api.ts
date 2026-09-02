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

// Runs produced by one schedule, newest first. Ordering is the server's:
// ListJobsByScheduledJobWithEmailPage orders `j.created_at DESC, j.id DESC`
// (internal/store/query/jobs.sql:77) over idx_jobs_sched_created_id.
//
// Deliberately NOT expressed through listJobs. ?sort= combined with ANY filter is a
// hard 400 - 'sort not supported on filtered list variant; remove the filter or
// remove the sort' (internal/api/jobs.go:417-422) - and listJobs sets sort by default
// on its unfiltered branch (:50-56). This function must never send sort. Do not
// "unify" the two.
//
// Auth runs BEFORE pagination (jobs.go:431-434): a non-owner non-admin gets a 404
// from ownedScheduledJob, not a paginated empty page, so a schedule's job ids cannot
// be enumerated by paging.
export function listJobsBySchedule(scheduledJobId: string, limit: number): Promise<JobsPage> {
  const q = new URLSearchParams({ scheduled_job_id: scheduledJobId, limit: String(limit) })
  return apiFetch<JobsPage>(`/jobs?${q}`)
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
  prev_seq: number
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
 * One page of a task's log history walking FORWARD from sinceSeq. next_seq is 0
 * when drained (internal/api/tasks.go:128-130). Always sends an explicit limit so
 * the caller is never silently truncated to the server default of 50.
 *
 * Sends no order parameter: order=desc here would return the NEWEST page where
 * the oldest is expected. Guard: api.test.ts 'getTaskLogs sends limit=200 and
 * omits since_seq on the first page'.
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

/**
 * One page of a task's log history walking BACKWARD. With no cursor this is the
 * newest page, which is how a log view opens at the end in one request rather
 * than paging forward through everything ahead of it.
 *
 * items are ASCENDING inside the page in both directions, so appendEntries and
 * the SSE dedupe consume this identically to a forward page. beforeSeq 0 means
 * "no cursor" and is omitted: the server 400s before_seq=0 rather than serving
 * an empty page, so a prev_seq of 0 means stop, not "ask again".
 */
export function getTaskLogsDesc(
  taskId: string,
  beforeSeq = 0,
  limit = BACKFILL_PAGE_SIZE,
): Promise<TaskLogPage> {
  const q = new URLSearchParams({ order: 'desc', limit: String(limit) })
  if (beforeSeq > 0) q.set('before_seq', String(beforeSeq))
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

// The two retry modes accepted by POST /v1/jobs/{id}/retry. `failed` reopens
// tasks in `failed` AND `timed_out`; `all` widens that to `done` as well
// (internal/store/query/tasks.sql, RetryJobTasks). There is no third value and no
// default: handleRetryJob 400s an absent, empty, repeated or unrecognized ?task
// rather than guessing, because a misread here means "re-ran everything".
export type RetryMode = 'failed' | 'all'

// What the caller is allowed to believe about a 200 from the retry endpoint.
//
// The response body is a full jobResponse plus this field, but it is built with
// `toJobResponse(job, "", nil, nil)` (internal/api/jobs.go, handleRetryJob), so
// `total_tasks`/`done_tasks` are ZERO, `tasks` is absent and `submitted_by_email`
// is absent. Writing that body into the ['job', id] cache would blank the task
// table on the page the user is looking at. `tasks_retried` is the only field
// that means anything here, and it is always >= 1 (a zero-match is a 409, never a
// successful no-op), so it is the only field this type exposes.
export interface RetryResult {
  tasks_retried: number
}

// Re-runs a finished job's tasks. Sends NO body - handleRetryJob never calls
// readJSON and ?task= is a query parameter, matching ?force= on cancelJob.
export function retryJob(id: string, mode: RetryMode): Promise<RetryResult> {
  const q = new URLSearchParams({ task: mode })
  return apiFetch<RetryResult>(`/jobs/${id}/retry?${q}`, { method: 'POST' })
}

// Creates a job from a raw parsed job-spec object. The client keeps the spec
// type permissive (unknown) and posts it verbatim; the server (ValidateJobSpec)
// is the validator of record, so new TaskSpec fields need no client change. The
// 201 body is a jobResponse; JobDetail is the closest existing type and carries
// the `id` the caller navigates to.
export function createJob(spec: unknown): Promise<JobDetail> {
  return apiFetch<JobDetail>('/jobs', { method: 'POST', json: spec })
}
