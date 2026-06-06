import { apiFetch } from '../lib/api'

// Matches the Go scheduledJobResponse field-for-field. job_spec is raw JSON and
// is not rendered in the list, so it stays `unknown`.
export interface Schedule {
  id: string
  name: string
  owner_id: string
  owner_email: string
  cron_expr: string
  timezone: string
  job_spec: unknown
  overlap_policy: string
  enabled: boolean
  next_run_at: string
  last_run_at?: string
  last_job_id?: string
  created_at: string
  updated_at: string
}

export interface SchedulesPage {
  items: Schedule[]
  next_cursor: string
  total: number
}

export type ScheduleSort =
  | '-created_at'
  | 'created_at'
  | 'name'
  | '-name'
  | 'next_run_at'
  | '-next_run_at'
  | 'updated_at'
  | '-updated_at'

// One page (limit=50). cursor advances to the next page when present.
export function listSchedules(sort: ScheduleSort, cursor?: string): Promise<SchedulesPage> {
  const q = new URLSearchParams({ sort, limit: '50' })
  if (cursor) q.set('cursor', cursor)
  return apiFetch<SchedulesPage>(`/scheduled-jobs?${q}`)
}

// Submits a fresh job from the stored job_spec. Allowed for the owner or an admin.
export function runScheduleNow(id: string): Promise<unknown> {
  return apiFetch(`/scheduled-jobs/${id}/run-now`, { method: 'POST' })
}

// Toggles the enabled flag via PATCH.
export function setScheduleEnabled(id: string, enabled: boolean): Promise<Schedule> {
  return apiFetch<Schedule>(`/scheduled-jobs/${id}`, { method: 'PATCH', json: { enabled } })
}
