import { apiFetch } from '../lib/api'

export type JobStatus =
  | 'pending'
  | 'queued'
  | 'dispatched'
  | 'running'
  | 'done'
  | 'failed'
  | 'timed_out'
  | 'cancelled'

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
