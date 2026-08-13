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
//
// The id is encoded: fetch() normalizes ".." path segments before dispatch, so an
// unencoded id like "../jobs/<uuid>" would turn this into a request against a
// DIFFERENT resource, e.g. POST /v1/jobs/<uuid>/run-now. encodeURIComponent turns
// the traversal segment's slashes into %2F, which a URL parser does not treat as
// path separators, keeping the request scoped under /v1/scheduled-jobs/.
export function runScheduleNow(id: string): Promise<unknown> {
  return apiFetch(`/scheduled-jobs/${encodeURIComponent(id)}/run-now`, { method: 'POST' })
}

// One scheduled job. NOTE the asymmetry with the list endpoint: handleGetScheduledJob
// (internal/api/scheduled_jobs.go:508-519) never calls fillOwnerEmails, unlike both
// list arms (:371, :504), and OwnerEmail has no omitempty (:25) - so `owner_email` is
// ALWAYS present and ALWAYS "" here. Callers must omit the owner rather than render an
// empty label, and must not substitute owner_id (36 opaque characters).
//
// Rejects with ApiError(404) both for a missing row and for a non-owner non-admin:
// ownedScheduledJob hides rather than refuses (:147-169). The two are indistinguishable
// on the wire by design; do not try to tell them apart.
// The id is encoded; see the note on runScheduleNow above - the same traversal
// risk applies to every call site in this file.
export function getSchedule(id: string): Promise<Schedule> {
  return apiFetch<Schedule>(`/scheduled-jobs/${encodeURIComponent(id)}`)
}

// Every field optional. An OMITTED key means "leave alone" server-side, because
// patchScheduledJobRequest is all pointers (internal/api/scheduled_jobs.go:521-528).
//
// SENDING A KEY YOU DID NOT CHANGE IS NOT A NO-OP. next_run_at is recomputed from
// time.Now() whenever the body merely CARRIES cron_expr or timezone, changed or not
// (:585, :595). Re-sending an unchanged cron on an `@every 1h` schedule whose next
// fire is five minutes away pushes that fire out by 55 minutes. Always build this
// from a diff against the loaded row, never from the whole form.
export interface SchedulePatch {
  name?: string
  cron_expr?: string
  timezone?: string
  overlap_policy?: string
  enabled?: boolean
  job_spec?: unknown
}

// 200 with the full updated row, including the recomputed next_run_at (:598-612).
// Concurrent edits are last-writer-wins: UpdateScheduledJob is a bare WHERE id = $1
// (internal/store/query/scheduled_jobs.sql:32-43) with no version column and there is
// no 409. The changed-fields-only body narrows the overlap to fields actually touched.
export function updateSchedule(id: string, patch: SchedulePatch): Promise<Schedule> {
  return apiFetch<Schedule>(`/scheduled-jobs/${encodeURIComponent(id)}`, { method: 'PATCH', json: patch })
}

// 204 with no body (internal/api/scheduled_jobs.go:633); apiFetch returns undefined for
// 204 (lib/api.ts:57), so no special handling is needed here.
//
// What it does to history: jobs.scheduled_job_id is ON DELETE SET NULL
// (internal/store/migrations/000006_scheduled_jobs.up.sql:20-21), so jobs the schedule
// already produced SURVIVE but are unlinked from it - the run history becomes
// unreachable. A run already in flight is not cancelled. That is the confirm copy.
export function deleteSchedule(id: string): Promise<void> {
  return apiFetch<void>(`/scheduled-jobs/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

// Toggles the enabled flag via PATCH. Expressed through updateSchedule so there is one
// PATCH client; the exported signature is byte-identical, so no call site and no
// existing test moved. It sends ONLY { enabled }: adding cron_expr or timezone here
// would recompute next_run_at on every toggle. Note the server recomputes anyway on a
// disabled -> enabled transition (internal/api/scheduled_jobs.go:585), which is the
// intended never-catch-up semantic.
export function setScheduleEnabled(id: string, enabled: boolean): Promise<Schedule> {
  return updateSchedule(id, { enabled })
}
