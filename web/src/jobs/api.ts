import { apiFetch } from '../lib/api'

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
  commands: string[][]
  env: Record<string, string>
  requires: Record<string, string>
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

// Static historical task log (GET, seq-paginated). Fetch-once; no tailing.
export function getTaskLogs(taskId: string, sinceSeq?: number): Promise<TaskLogPage> {
  const q = new URLSearchParams()
  if (sinceSeq !== undefined) q.set('since_seq', String(sinceSeq))
  const qs = q.toString()
  return apiFetch<TaskLogPage>(`/tasks/${taskId}/logs${qs ? `?${qs}` : ''}`)
}
