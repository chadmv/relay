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
  // Why the last SCHEDULER fire failed, and when. ABSENT MEANS HEALTHY - the
  // server omits both keys entirely (scheduledJobResponse carries `omitempty` on
  // each), never "" and never null, so `schedule.last_error ? ... : null` is the
  // correct and only test. A schedule that has never failed renders exactly as
  // it did before these fields existed.
  //
  // THE TEXT IS OPERATOR-SUPPLIED, and partly attacker-chosen in the admin case:
  // it is derived from the stored job_spec and embeds a task name the schedule's
  // owner picked, and an admin can read any user's schedule. The server strips
  // control characters and truncates it, and the SPA must render it as a React
  // TEXT CHILD inside a panel whose heading names its provenance - never as
  // chrome, never through dangerouslySetInnerHTML, and never into a URL, a title
  // attribute or a log line. Same rule, same reason, as the Job spec panel on the
  // detail page.
  last_error?: string
  last_error_at?: string
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

// One scheduled job. `owner_email` is resolved here exactly as on the list endpoint -
// handleGetScheduledJob and both list arms all go through fillOwnerEmails. That helper
// is best-effort though: it logs and leaves the field "" when the owner lookup fails,
// and OwnerEmail carries no omitempty, so the key is ALWAYS present and may still be
// empty. Callers must omit the owner rather than render an empty label when it is, and
// must not substitute owner_id (36 opaque characters).
//
// Rejects with ApiError(404) both for a missing row and for a non-owner non-admin:
// ownedScheduledJob hides rather than refuses. The two are indistinguishable
// on the wire by design; do not try to tell them apart.
// The id is encoded; see the note on runScheduleNow above - the same traversal
// risk applies to every call site in this file.
export function getSchedule(id: string): Promise<Schedule> {
  return apiFetch<Schedule>(`/scheduled-jobs/${encodeURIComponent(id)}`)
}

// Every field optional. An OMITTED key means "leave alone" server-side, because
// patchScheduledJobRequest is all pointers.
//
// SENDING A KEY YOU DID NOT CHANGE IS NOT A NO-OP, AND THERE ARE NOW TWO
// CONSEQUENCES. next_run_at is recomputed from time.Now() whenever the body
// merely CARRIES cron_expr or timezone, changed or not - see
// handlePatchScheduledJob - so re-sending an unchanged cron on an `@every 1h`
// schedule whose next fire is five minutes away pushes that fire out by 55
// minutes. AND a body carrying job_spec, cron_expr or timezone CLEARS
// last_error/last_error_at, on the reasoning that the handler validated the new
// values before storing them so any record about the OLD ones is stale by
// construction. Re-sending an unchanged cron therefore also erases the only
// signal that the schedule is broken. Always build this from a diff against the
// loaded row, never from the whole form.
export interface SchedulePatch {
  name?: string
  cron_expr?: string
  timezone?: string
  overlap_policy?: string
  enabled?: boolean
  job_spec?: unknown
}

// 200 with the full updated row, including the recomputed next_run_at - see
// handlePatchScheduledJob.
// Concurrent edits are last-writer-wins: UpdateScheduledJob is a bare WHERE id = $1
// (internal/store/query/scheduled_jobs.sql) with no version column and there is
// no 409. The changed-fields-only body narrows the overlap to fields actually touched.
export function updateSchedule(id: string, patch: SchedulePatch): Promise<Schedule> {
  return apiFetch<Schedule>(`/scheduled-jobs/${encodeURIComponent(id)}`, { method: 'PATCH', json: patch })
}

// 204 with no body (handleDeleteScheduledJob); apiFetch returns undefined for
// 204 (lib/api.ts), so no special handling is needed here.
//
// What it does to history: jobs.scheduled_job_id is ON DELETE SET NULL
// (internal/store/migrations/000006_scheduled_jobs.up.sql), so jobs the schedule
// already produced SURVIVE but are unlinked from it - the run history becomes
// unreachable. A run already in flight is not cancelled. That is the confirm copy.
export function deleteSchedule(id: string): Promise<void> {
  return apiFetch<void>(`/scheduled-jobs/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

// Toggles the enabled flag via PATCH. Expressed through updateSchedule so there is one
// PATCH client; the exported signature is byte-identical, so no call site and no
// existing test moved. It sends ONLY { enabled }: adding cron_expr or timezone here
// would recompute next_run_at on every toggle. Note the server recomputes anyway on a
// disabled -> enabled transition (handlePatchScheduledJob), which is the
// intended never-catch-up semantic.
export function setScheduleEnabled(id: string, enabled: boolean): Promise<Schedule> {
  return updateSchedule(id, { enabled })
}
