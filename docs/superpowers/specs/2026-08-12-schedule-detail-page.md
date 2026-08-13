# Schedule Detail Page and Edit Action - Design

Date: 2026-08-12
Status: Draft (autonomous cycle; conductor review)

## Overview

`/schedules` lists schedules and offers Run now / Enable / Disable, but there is no
way to open one, read its job spec, or change its cron. This slice adds a
`/schedules/:id` detail page and the list entry point that reaches it.

Backlog item: `docs/backlog/closed/idea-2026-06-05-schedule-detail-page.md` (closed by this slice).
Design source of truth: the hi-fi `HoloScheduleDetail`
(`design_handoff_relay_holo/hifi3-holo-pages.jsx:1652-1871`). The `reference/`
sketch is structure-only.

Frontend-only. No backend change, no new endpoint, no store query.

Written in autonomous gate mode: every design question below was decided here and
carries a one-line rationale in the Decisions section rather than being asked.

## Where the backlog item and ROADMAP were wrong or incomplete

The item's Proposal is two sentences. Verified against the code, four of its
implications are wrong or materially incomplete, and two change the shape of the
work:

1. **"wire the list's Edit action to it" - there is no Edit action.**
   `SchedulesTable.tsx:72-89` renders exactly two buttons per row, `Run now` and
   `Disable`/`Enable`. The hi-fi list row has `Run now`/`Enable` **plus** `Edit`
   (`hifi3-holo-pages.jsx:1610-1617`). The entry point has to be built, not wired.
2. **The recent-runs panel is NOT backend-blocked, contrary to the assumption that
   made it look like a stretch goal.** `GET /v1/jobs?scheduled_job_id=<uuid>` is a
   real, auth-gated, cursor-paginated branch (`internal/api/jobs.go:424-454`) over an
   index built for it (`migrations/000011_pagination_indexes.up.sql:8`). Every column
   the hi-fi draws is a real field. Scoping this panel out would have been wrong.
   It also carries a trap the item does not mention: `?sort=` combined with the
   filter is a hard **400** (`internal/api/jobs.go:417-422`).
3. **The hi-fi's overlap control offers three values; the backend accepts two.**
   `skip | allow | queue` in the design (`hifi3-holo-pages.jsx:1773`) against
   `overlap_policy must be 'skip' or 'allow'` in the handler
   (`internal/api/scheduled_jobs.go:561-564`, and the same check on create at
   `:94-97`). A `queue` button would be a control that always 400s.
4. **`GET /v1/scheduled-jobs/{id}` does not return the owner's email**, though the
   list endpoint does. `handleGetScheduledJob` (`internal/api/scheduled_jobs.go:508-519`)
   calls `toScheduledJobResponse` and never `fillOwnerEmails`, unlike both list arms
   (`:371` admin, `:504` owner-scoped). `OwnerEmail` has no `omitempty` (`:25`), so
   the detail response always contains `"owner_email": ""`. The hi-fi's identity
   sub-line shows `owner <email>` (`hifi3-holo-pages.jsx:1735`). See decision 8.

Verified correct in the item: the route shape, that `GET` and `PATCH
/v1/scheduled-jobs/{id}` exist, that the work is frontend-only, and the four-panel
description of the Holo page.

## Verified backend contract

Routes, from `internal/api/server.go:163-168`. All six are `auth(...)`; **none** is
`AdminOnly`:

```go
mux.Handle("GET /v1/scheduled-jobs/{id}",         auth(http.HandlerFunc(s.handleGetScheduledJob)))
mux.Handle("PATCH /v1/scheduled-jobs/{id}",       auth(http.HandlerFunc(s.handlePatchScheduledJob)))
mux.Handle("DELETE /v1/scheduled-jobs/{id}",      auth(http.HandlerFunc(s.handleDeleteScheduledJob)))
mux.Handle("POST /v1/scheduled-jobs/{id}/run-now", auth(http.HandlerFunc(s.handleRunScheduledJobNow)))
```

### Authorization

Every one of the four routes above, and the `?scheduled_job_id=` jobs branch, funnels
through `ownedScheduledJob` (`internal/api/scheduled_jobs.go:147-169`):

```go
if !u.IsAdmin && row.OwnerID != u.ID {
    writeError(w, http.StatusNotFound, "scheduled job not found")
    return store.ScheduledJob{}, false
}
```

Owner **or** admin. A non-owner non-admin gets **404**, not 403 - the resource is
hidden, not refused. A missing row is the same 404 (`:157-159`). This is the entire
authorization story for the page; the SPA adds no gate of its own and must not.

### GET /v1/scheduled-jobs/{id} - response, verbatim

`scheduledJobResponse`, `internal/api/scheduled_jobs.go:19-34`:

```go
type scheduledJobResponse struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	OwnerID       string          `json:"owner_id"`
	OwnerEmail    string          `json:"owner_email"`
	CronExpr      string          `json:"cron_expr"`
	Timezone      string          `json:"timezone"`
	JobSpec       json.RawMessage `json:"job_spec"`
	OverlapPolicy string          `json:"overlap_policy"`
	Enabled       bool            `json:"enabled"`
	NextRunAt     time.Time       `json:"next_run_at"`
	LastRunAt     *time.Time      `json:"last_run_at,omitempty"`
	LastJobID     string          `json:"last_job_id,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}
```

Nullability, from `toScheduledJobResponse` (`:36-58`):

- `last_run_at` and `last_job_id` keys are **absent** when the columns are NULL
  (`omitempty` on a pointer / on the empty string). Not `null`.
- `next_run_at` is `NOT NULL` in the schema
  (`migrations/000006_scheduled_jobs.up.sql:10`) and always present.
- `owner_email` is **always present and always `""` on this endpoint** (decision 8).
- `job_spec` is opaque JSON passed through `rawJSON`. The client keeps it `unknown`,
  as the shipped list type already does (`web/src/schedules/api.ts:12`).
- Timestamps are Go `time.Time`, RFC3339 with nanoseconds. Parse with `new Date()`,
  never string-compare.

The shipped `Schedule` interface (`web/src/schedules/api.ts:5-20`) matches this
struct field-for-field and is reused unchanged for the detail page.

### PATCH /v1/scheduled-jobs/{id} - what it actually accepts

`patchScheduledJobRequest`, `internal/api/scheduled_jobs.go:521-528`. **The item's
implicit assumption that PATCH covers cron + tz + overlap is correct, and it covers
three more fields besides:**

```go
type patchScheduledJobRequest struct {
	Name          *string          `json:"name"`
	CronExpr      *string          `json:"cron_expr"`
	Timezone      *string          `json:"timezone"`
	OverlapPolicy *string          `json:"overlap_policy"`
	Enabled       *bool            `json:"enabled"`
	JobSpec       *json.RawMessage `json:"job_spec"`
}
```

Every field is a pointer, so an omitted key means "leave alone" and is distinguished
from a zero value (`:546-582`). Validation, in handler order:

| Field | Rule | Failure |
|---|---|---|
| `name` | none - `""` is accepted | - |
| `overlap_policy` | `"skip"` or `"allow"` only | 400 `overlap_policy must be 'skip' or 'allow'` (`:561-564`) |
| `job_spec` | `json.Unmarshal` into `JobSpec`, then `ValidateJobSpec` | 400 `invalid job_spec JSON`, or the validator's own message (`:570-582`) |
| `cron_expr` / `timezone` | `ValidateMinInterval(cron, tz, 30s)` then `ParseSchedule` | 400, message below (`:584-596`) |

The cron and timezone messages come from `internal/schedrunner/cron.go`:

- `invalid timezone %q: <LoadLocation error>` (`cron.go:35`)
- `invalid cron expression %q: <parser error>` (`cron.go:39`)
- `schedule fires faster than minimum interval 30s (observed <d>)` (`cron.go:58`,
  with `minScheduleInterval = 30 * time.Second` at `scheduled_jobs.go:17`)

The accepted cron grammar is 5-field cron plus descriptors plus `@every <dur>`
(`cron.go:14-16`), evaluated in an IANA location loaded by name - **any** valid IANA
name, not a fixed list.

**The one non-obvious side effect, and it drives decision 4.** `next_run_at` is
recomputed from `time.Now()` whenever the request *carries* a cron or timezone key,
regardless of whether the value changed (`scheduled_jobs.go:584-596`):

```go
if req.CronExpr != nil || req.Timezone != nil || (req.Enabled != nil && *req.Enabled && !row.Enabled) {
	...
	nextRunAt = pgtype.Timestamptz{Time: sched.Next(time.Now()), Valid: true}
}
```

So re-sending an unchanged `cron_expr` on an `@every 1h` schedule whose next fire is
five minutes away pushes that fire out by 55 minutes. Re-enabling a disabled schedule
also recomputes, which is the intended never-catch-up semantic
(`ReconcileOnStartup`'s rule, per CLAUDE.md).

Success is **200** with the full updated row (`:598-612`), including the recomputed
`next_run_at`. There is no 409 and no optimistic-concurrency token: `UpdateScheduledJob`
is a bare `WHERE id = $1` (`internal/store/query/scheduled_jobs.sql:32-43`), so
concurrent edits are last-writer-wins.

### DELETE /v1/scheduled-jobs/{id}

`handleDeleteScheduledJob` (`internal/api/scheduled_jobs.go:615-634`): owner-or-admin,
**204** with no body, 404 if the row vanished between the ownership check and the
delete, 500 `delete failed`.

What it does to history: `jobs.scheduled_job_id` is
`REFERENCES scheduled_jobs(id) ON DELETE SET NULL`
(`migrations/000006_scheduled_jobs.up.sql:20-21`). Jobs already produced by the
schedule **survive**, but their link to it is severed - the run history becomes
unreachable and the `⟳ <name>` badge on the jobs list disappears. That is the confirm
dialog's copy (decision 9). Delete does not cancel a run in flight.

### POST /v1/scheduled-jobs/{id}/run-now

`handleRunScheduledJobNow` (`internal/api/scheduled_jobs.go:636-679`): owner-or-admin
(not admin-only, contrary to the hi-fi's footnote at `hifi3-holo-pages.jsx:1634`),
**201** with a full job body, and the job is attributed to the **schedule's owner**,
not the calling admin (`:661-666`). Already wired in the SPA via
`runScheduleNow` (`web/src/schedules/api.ts:46-48`).

### GET /v1/jobs?scheduled_job_id={id} - the recent-runs source

`internal/api/jobs.go:424-454`. This is the panel the item's Proposal is silent about,
and it is fully supported.

- **Auth runs before pagination** (`:431-434`): `ownedScheduledJob` is called first, so
  a non-owner gets a 404 rather than a paginated empty page. Deliberate, and commented
  as such in the handler.
- Backed by `ListJobsByScheduledJobWithEmailPage`
  (`internal/store/query/jobs.sql:60-78`), ordered `j.created_at DESC, j.id DESC`
  (`:77`) - newest run first, which is what the panel wants - over
  `idx_jobs_sched_created_id` (`migrations/000011_pagination_indexes.up.sql:8`).
- `total` comes from `CountJobsByScheduledJob` (`jobs.sql:80-81`).
- **`?sort=` must not be sent.** `hasSort && hasFilter` is a 400 with
  `sort not supported on filtered list variant; remove the filter or remove the sort`
  (`internal/api/jobs.go:417-422`). `parsePage` still runs first, so `?limit=` and
  `?cursor=` are honoured normally.
- Row shape is the list-enriched `jobResponse` (`internal/api/jobs.go:55-73`):
  `status`, `started_at`, `finished_at`, `total_tasks`, `done_tasks`,
  `submitted_by_email`, `scheduled_job_id`, `scheduled_job_name`. `started_at` /
  `finished_at` keys are **absent** when the job has no started/finished task
  (`applyJobEnrichment`, `:119-137`). The client `Job` type
  (`web/src/jobs/api.ts:5-20`) already models all of this.

So the hi-fi's `STARTED | DURATION | STATUS | JOB ID | OWNER` columns
(`hifi3-holo-pages.jsx:1841`) are each backed by a real field. Nothing in this panel
is fabricated.

## Existing frontend this must match

| Thing | Precedent |
|---|---|
| Detail-page skeleton: breadcrumb + name + status pill + `ml-auto` action bar, mono identity sub-line, two-column `grid-cols-2 gap-3` body of `Panel`s | `web/src/workers/WorkerDetailPage.tsx:70-208` |
| Loading `<GlassPanel className="h-40" />`, and an error card that distinguishes `ApiError.status === 404` from a retryable error, with a back link | `web/src/workers/WorkerDetailPage.tsx:30-55` |
| Omit-rather-than-fake, with the enabler item named in a code comment | `web/src/workers/WorkerDetailPage.tsx:105-110,117-118,140-141`; `web/src/schedules/SchedulesPage.tsx:127-137` |
| Inline edit form on a detail page, building a patch from **changed fields only** | `web/src/workers/WorkerEditForm.tsx:17-46`, especially `:42-45` |
| Row link on a list's primary identifier | `web/src/jobs/JobsTable.tsx:46`, `web/src/workers/WorkersTable.tsx:46` |
| Job status dot/colour, progress, duration, absolute start | `web/src/jobs/status.ts:11-54` |
| Table primitive with `role="table"` semantics; footer rendered **outside** the table subtree | `web/src/components/holo/Table.tsx` via `web/src/jobs/JobsTable.tsx:32-80` |
| Destructive action behind `ConfirmDialog` (which composes `DialogShell`/`dialogStack`) | `web/src/components/ConfirmDialog.tsx:17-61` |
| Local clock tick with zero requests | `web/src/lib/useNow.ts:8-15` |
| Relative "in 4h"/"due" and short id | `web/src/schedules/format.ts:4-13,16-19` |
| Single fetch entry point | `web/src/lib/api.ts:29-59` (`apiFetch`, `ApiError`) |
| Read-only / editable JSON as a mono textarea or `<pre>`, never YAML | `web/src/jobs/NewJobPage.tsx:51-59` |

Available Holo primitives: `GlassPanel, Eyebrow, ProgressBar, Chip, PillButton,
KpiStat, Panel, StatusDot, Table, TableRow, TableCell, ariaSort, sortCaret`
(`web/src/components/holo/index.ts:3-12`). No new primitive is needed.

`web/package.json:13-20` - runtime dependencies are exactly
`@fontsource-variable/jetbrains-mono`, `@fontsource-variable/space-grotesk`,
`@tanstack/react-query`, `react`, `react-dom`, `react-router-dom`. **There is no cron
parser, no YAML library, and no date library.** This is the fact behind decisions 1
and 5.

## Panels, and what each is scoped to

### Header and identity line

Breadcrumb `← Schedules / <name>`, an `ENABLED`/`PAUSED` pill, and a right-aligned
action bar: `Run now`, `Enable`/`Disable`, `Delete`. Matches
`hifi3-holo-pages.jsx:1707-1739` minus the owner field (decision 8).

Sub-line (mono, `text-fg-mute`): `created <abs>`, `updated <rel>`,
`next fire <rel>` (`-` when disabled), `last run <rel>` and a `last job` link when
`last_job_id` is present. All from the GET payload.

### Trigger panel (editable) - left column, top

Cron text input, timezone text input, overlap two-button segmented control,
`Save changes` / `Cancel`. Maps to `hifi3-holo-pages.jsx:1745-1791`. Scoped to the
three fields the item names. `name`, `enabled` and `job_spec` are also PATCH-able but
are **not** in this form: `enabled` has its own header button, and `name` and
`job_spec` are decisions 5 and 10.

### Job spec panel (read-only) - left column, bottom

`<pre>` of `JSON.stringify(schedule.job_spec, null, 2)`. Maps to
`hifi3-holo-pages.jsx:1793-1804` minus its YAML rendering and minus its `Edit` button
(decision 5).

### Next fire panel - right column, top

**One** entry, the server-supplied `next_run_at`, rendered as relative + absolute,
dimmed with `paused - no fires queued` when `enabled` is false. Maps to
`hifi3-holo-pages.jsx:1809-1829`, reduced from five entries to one (decision 1).

### Recent runs panel - right column, bottom

`GET /v1/jobs?scheduled_job_id={id}&limit=20`, newest first. Columns
`STARTED | DUR | STATUS | JOB ID | OWNER`, job id linking to `/jobs/{id}`. Maps to
`hifi3-holo-pages.jsx:1831-1866`. Fully real (decision 3).

## Decisions

**1. Next fires shows one entry, the backend's `next_run_at`. No JS cron dependency.**
A five-entry preview needs a cron parser; `web/` has none, so it would mean shipping a
*second* implementation of `@every`, `@hourly` and IANA-zone semantics that has to
agree with `robfig/cron/v3` (`cron.go:14-16`) - and a preview that silently disagrees
is worse than one honest value, since the point of the panel is trust. The single
value is not a degraded placeholder: `PATCH` recomputes `next_run_at` server-side and
returns it (`scheduled_jobs.go:584-596`), so after a save the panel shows the
authoritative first fire of the edit the user just made. Scoped out: entries 2..N.

**2. The edit surface is inline on the page, not a dialog.** The hi-fi puts it inline
(`hifi3-holo-pages.jsx:1745-1791`), the shipped precedent for editing a resource from
its detail page is `WorkerEditForm` (`web/src/workers/WorkerEditForm.tsx`), and all
three current `DialogShell` consumers are a confirmation or a secret reveal
(`ConfirmDialog`, `ResetPasswordDialog`, `TokenRevealDialog`) - none is a multi-field
resource editor. `DialogShell` is still used on this page, via `ConfirmDialog`, for
Delete.

**3. The recent-runs panel ships, as a fixed latest-20 window with no pager.** The
endpoint exists and every column is real, so scoping it out would have been the wrong
call. It is a summary on a detail page, not a list page, so it takes `limit=20`, no
cursor stack, and a footer reading `latest N of <total>` from the response envelope -
which keeps the panel honest about being a window without importing the
`cursorStack`/`offsets`/`computePageRange` machinery. The request sends
`scheduled_job_id` and `limit` and **never** `sort` (`internal/api/jobs.go:417-422`).

**4. The PATCH body carries only changed fields.** Not a style preference: sending an
unchanged `cron_expr` recomputes `next_run_at` from now
(`scheduled_jobs.go:584-596`) and can delay the next fire by nearly a full interval.
Same construction as `WorkerEditForm.tsx:42-45`. If nothing changed, Save is a no-op
and issues no request.

**5. The job spec is read-only JSON, not YAML, and has no Edit control.** The stored
value is JSON (`scheduledJobResponse.JobSpec` is `json.RawMessage`,
`scheduled_jobs.go:26`); `web/` has no YAML serializer; and the app's only spec editor
is already a JSON textarea (`NewJobPage.tsx:51-59`). Editing it here would duplicate
that surface plus its `validateSpecText` for a second resource, which is its own
slice. Scoped out with an enabler filed (see below).

**6. The overlap control renders two options, `skip` and `allow`.** The hi-fi's third
option, `queue`, always 400s (`scheduled_jobs.go:561-564`). No enabler is filed: a
queueing overlap policy is a scheduler product decision, not a UI gap - the same
treatment `selector` got in the reservations spec.

**7. Timezone is a free-text input, not the hi-fi's six-entry dropdown.** The backend
accepts any name `time.LoadLocation` resolves (`cron.go:33-36`); a fixed six-entry
`<select>` (`hifi3-holo-pages.jsx:1766`) would make an existing schedule's zone
unselectable and silently rewrite it on the next save. A non-constraining `<datalist>`
of common zones provides the convenience without the trap.
`Intl.supportedValuesOf('timeZone')` was **not** verified against this repo's jsdom
version and must not be relied on.

Corollary, and this is why 1 and 7 are the same decision: **there is no client-side
cron or timezone validation at all.** The server is the validator of record and its
400 message is surfaced verbatim. A client-side pre-check would be the second cron
implementation decision 1 exists to avoid.

**8. The owner is omitted from the identity line rather than faked.** `GET
/v1/scheduled-jobs/{id}` returns `"owner_email": ""` for every caller
(`scheduled_jobs.go:508-519` never calls `fillOwnerEmails`; `:25` has no `omitempty`).
The page renders the owner line only when `owner_email` is non-empty - so today, never
- and does **not** fall back to `owner_id`, which is 36 opaque characters. Explicitly
rejected: carrying the value over from the cached list row. The page must behave
identically on a deep link or hard refresh, and a field that appears only when you
arrive by one route is worse than a consistent omission. Enabler filed.

**9. Delete ships on the detail page, behind `ConfirmDialog` (destructive).** The
endpoint is real (`scheduled_jobs.go:615-634`) and the hi-fi puts the control here
(`hifi3-holo-pages.jsx:1730`). The list stays delete-free - one destructive control,
on the page where you can read what you are about to destroy. Confirm copy states the
verified consequence: past jobs are kept but **unlinked** from this schedule and its
run history becomes unreachable (`ON DELETE SET NULL`,
`migrations/000006_scheduled_jobs.up.sql:20-21`), and a run already in flight is not
cancelled. On 204 the page navigates to `/schedules`.

**10. `name` is not editable in this slice.** PATCH accepts it, but the hi-fi's
Trigger panel does not offer it and renaming from a page whose breadcrumb is the name
raises questions (does the URL change? does history rewrite?) that are not worth
answering for a field nothing depends on. Deliberately deferred, not overlooked; no
enabler - it is one more input whenever someone wants it.

**11. Query keys nest under the existing `['schedules']` prefix.**
`['schedules', 'detail', id]` and `['schedules', 'runs', id]`, so the bare-prefix
invalidation the shipped `useScheduleActions` already performs
(`useScheduleActions.ts:11,16`) reaches them with **no change to that shared hook's
existing mutations**. `'detail'`/`'runs'` cannot collide with `useSchedules`'
`['schedules', sort, cursor]` (`useSchedules.ts:10`) because neither is a
`ScheduleSort` value (`api.ts:28-36`).

**12. The detail and runs queries poll at 10s; the countdown ticks locally at 1s.**
10s matches `useSchedules` (`useSchedules.ts:8`) and this data is equally low-churn;
`useJobs`' 3s (`useJobs.ts:7`) is for a live fleet view. The 1s tick uses the shared
`useNow(1000)` (`web/src/lib/useNow.ts:8-15`) rather than re-implementing
`SchedulesPage`'s ad-hoc `setTick` (`SchedulesPage.tsx:43-47`) - same behaviour, no
new local timer idiom.

**13. The list's entry point is an `Edit` link in ACTIONS plus a link on NAME.** The
`Edit` button matches the hi-fi row (`hifi3-holo-pages.jsx:1616`); the NAME link
matches the shipped house pattern in `JobsTable.tsx:46` and `WorkersTable.tsx:46` and
is the discoverable route. Both are react-router `<Link>`s, not `useNavigate`
handlers, so middle-click and open-in-new-tab work and no callback has to be threaded
through `SchedulesTable`'s props.

## Architecture

New files, all under `web/src/schedules/`:

- `ScheduleDetailPage.tsx` - route component: loading/404/error triad, header, action
  bar, identity line, two-column body.
- `ScheduleTriggerForm.tsx` - the cron/tz/overlap inline form (decision 2), owning its
  own draft state and changed-field diff (decision 4).
- `ScheduleRunsPanel.tsx` - the recent-runs table (decision 3).
- `useSchedule.ts` - `useQuery(['schedules','detail',id])`, `refetchInterval: 10000`,
  `placeholderData: keepPreviousData`.
- `useScheduleRuns.ts` - `useQuery(['schedules','runs',id])`, same interval.

Modified:

- `web/src/app/router.tsx` - one route `<Route path="/schedules/:id"
  element={<ScheduleDetailPage />} />` inside `ProtectedRoute`, beside the existing
  `/schedules` (`router.tsx:31`). No `AdminRoute`: the endpoints are owner-or-admin.
- `web/src/schedules/api.ts` - add `getSchedule(id)`, `SchedulePatch`,
  `updateSchedule(id, patch)`, `deleteSchedule(id)`. `setScheduleEnabled`
  (`api.ts:51-53`) is re-expressed as `updateSchedule(id, { enabled })`, keeping its
  exported signature byte-identical so no call site or existing test moves.
- `web/src/schedules/useScheduleActions.ts` - add `update` and `remove` mutations
  alongside `runNow`/`setEnabled`, invalidating the same bare `['schedules']` prefix.
  Purely additive; the two existing mutations are untouched.
- `web/src/schedules/SchedulesTable.tsx` - NAME becomes a `<Link>`; an `Edit` `<Link>`
  joins the ACTIONS cell (decision 13).
- `web/src/jobs/api.ts` - add `listJobsBySchedule(scheduleId, limit)` next to
  `listJobs` (`api.ts:50-56`), returning the existing `JobsPage`. It lives here, not
  in `schedules/api.ts`, because it is a `/jobs` endpoint returning the `Job` type;
  `listJobs` is left alone because its sort/status branching is exactly what this call
  must not do.

Reused unchanged: `GlassPanel`, `Panel`, `Chip`, `PillButton`, `Table`/`TableRow`/
`TableCell`, `ConfirmDialog`, `Field`, `Input`, `Button`, `apiFetch`, `ApiError`,
`useNow`, `formatRelativeTime`, `nextRunDisplay`, `shortId`, `statusColor`,
`formatDuration`, `formatStarted`.

### Exact calls

```
getSchedule(id)              -> GET    /v1/scheduled-jobs/{id}              -> Schedule (200)
updateSchedule(id, patch)    -> PATCH  /v1/scheduled-jobs/{id}   json:patch -> Schedule (200)
deleteSchedule(id)           -> DELETE /v1/scheduled-jobs/{id}              -> void     (204)
listJobsBySchedule(id, 20)   -> GET    /v1/jobs?scheduled_job_id={id}&limit=20 -> JobsPage (200)
```

`SchedulePatch` is `{ name?, cron_expr?, timezone?, overlap_policy?, enabled?,
job_spec? }`, all optional; the Trigger form only ever sets the middle three, and only
when changed. `apiFetch` already returns `undefined` for 204 (`lib/api.ts:56-57`), so
`deleteSchedule` needs no special handling.

### Interaction detail

| Control | Behaviour |
|---|---|
| Schedules row `NAME` / `Edit` | Navigate to `/schedules/{id}`. |
| `Run now` | `runNow.mutateAsync(id)`; invalidating `['schedules']` refreshes both the detail row and the runs panel, so the new job appears without a reload. |
| `Enable` / `Disable` | `setEnabled`; note the server recomputes `next_run_at` on a disabled -> enabled transition. |
| `Delete` | `ConfirmDialog` (destructive) -> `remove.mutate(id)` -> `navigate('/schedules')` on success. |
| Trigger `Save changes` | Diffs against the loaded schedule; no-op when clean; disabled while pending. |
| Trigger `Cancel` | Resets the draft to the currently loaded values and clears the error. |
| Errors | List/detail load errors use the 404-vs-retryable card; PATCH and DELETE errors render in an inline `role="alert"` box next to the control that caused them, never in a page-level box behind a scrim. |

Two lifecycle rules, both instances of house invariants:

- **A poll must not clobber a dirty form.** The Trigger form seeds its draft state
  from the schedule once and never re-derives it on re-render, so a 10s refetch
  landing mid-edit cannot overwrite typed text. Its state is reset only by an explicit
  Cancel or by a successful save.
- **A settled mutation must not write into a form that has moved on.** `onSuccess`
  closes/marks-clean and invalidates; it never pushes the response back into draft
  state. This is the local shape of "end the generation before releasing the
  resource" - a late response must not reanimate state a later action already
  replaced.

## Scoped out, with the enabler to file in Phase 6

| Hi-fi / item element | Why it is out | Enabler to file |
|---|---|---|
| Next fires entries 2..N (`hifi3:1676-1682,1814-1828`) | Needs a cron parser in `web/`; a second implementation that disagrees with `robfig/cron/v3` is worse than one true value (decision 1). | **File:** `idea-2026-08-12-schedule-next-fires-preview.md` - a server-computed `next_fires` array (or `?preview=N`) on `GET /v1/scheduled-jobs/{id}`, using the authoritative parser. Frontend then renders N rows with no new dependency. |
| Owner on the identity line (`hifi3:1735`) | `GET /v1/scheduled-jobs/{id}` returns `owner_email: ""` while both list arms populate it (decision 8). | **File:** `bug-2026-08-12-scheduled-job-detail-missing-owner-email.md` (low) - the same response struct is populated by one handler and not the other; fix is one `fillOwnerEmails` call in `handleGetScheduledJob`. |
| Job-spec `Edit` button (`hifi3:1799`) | PATCH accepts `job_spec`, but an editor duplicates `NewJobPage`'s textarea + `validateSpecText` for a second resource (decision 5). | **File:** `idea-2026-08-12-schedule-job-spec-editor.md` - frontend-only; extract the spec editor from `NewJobPage` into a shared component and reuse it here. |
| `queue` overlap option (`hifi3:1773`) | Backend accepts only `skip`/`allow` (decision 6). | **None.** A queueing policy is a scheduler product decision, not a UI gap. |
| `explainCron(...)` human gloss (`hifi3:1758`) | A cron *explainer* is a cron parser by another name - decision 1 again. | **None** (folded into the next-fires item: a server-side gloss could ride along). |
| `Pause`/`Resume` as distinct endpoints (`hifi3:1726-1727`) | No such routes exist; the real mechanism is `PATCH { enabled }`, already shipped (`api.ts:51-53`). The buttons read `Enable`/`Disable` to match the list. | **None.** |
| A pager on recent runs | Deliberate: a summary window, not a list page (decision 3). | **None.** |
| Renaming from the detail page | Deferred (decision 10). | **None.** |
| Filter chips / search / FAILED-24H on the **list** | Untouched by this slice; already scoped out with items filed (`SchedulesPage.tsx:127-137`). | Already filed: `idea-2026-06-05-schedules-filter-search.md`, `idea-2026-06-05-failed-24h-stat.md`, `idea-2026-06-05-schedules-stats-endpoint.md`. |

Per the standing rule, these are **proposals**. Phase 6 files them for human accept;
nothing is auto-filed.

## Security and system design

- **Threat model.** No new endpoint and no widened surface. Authorization is entirely
  `ownedScheduledJob` (`scheduled_jobs.go:147-169`), owner-or-admin, 404-on-deny, and
  it also gates the runs query *before* pagination (`jobs.go:431-434`), so a non-owner
  cannot enumerate a schedule's job ids by paging. The SPA's route is behind
  `ProtectedRoute` only; a forged client state buys a 404, not data.
- **Disclosure.** `job_spec` can contain `env` values that a user chose to store, and
  this page renders it verbatim. It is not a new disclosure - the same bytes are
  already in every list response to the same audience (`scheduled_jobs.go:43`) - but
  the rendering must stay a React text child in a `<pre>`; no
  `dangerouslySetInnerHTML`, and nothing from `job_spec` is ever put in a URL, a
  `title`, or a log line.
- **Availability / blast radius.** The riskiest write here is a cron edit. The server
  caps frequency at one fire per 30s (`minScheduleInterval`, `scheduled_jobs.go:17`,
  enforced by `ValidateMinInterval`, `cron.go:46-61`), so a fat-fingered `* * * * *`
  is accepted but bounded, and `overlap_policy: skip` bounds concurrent instances.
  Delete is the irreversible action; it is confirmed, and it destroys no job data
  (`ON DELETE SET NULL`).
- **Concurrent edits are last-writer-wins.** `UpdateScheduledJob` is a bare
  `WHERE id = $1` (`scheduled_jobs.sql:32-43`) with no version column, so two admins
  editing one schedule silently overwrite each other. Not fixed here; the changed-
  fields-only body (decision 4) narrows the overlap to fields actually touched, and
  the 10s poll surfaces the other party's result quickly. Called out rather than
  papered over.
- **Load.** Per open detail page: two polled queries at 10s (one indexed point lookup,
  one indexed 20-row cursor page over `idx_jobs_sched_created_id`), plus one 1s local
  timer that issues **no** request. Mutations are user-initiated. Nothing here polls
  faster than the list page it was opened from.
- **Invariants.** No backend change: epoch fence, single job-spec pipeline, one
  bounded sender per stream, identity-checked teardown, no interior pointers across
  locks, and single JSON entry point are all untouched. Note that job creation from
  this page still flows through the single job-spec pipeline - `Run now` calls
  `CreateJobFromSpec` server-side (`scheduled_jobs.go:664`), and this slice adds no
  parallel spec path. Frontend analogues that do apply: every request goes through
  `apiFetch`, and the two lifecycle rules above.

## Testing

Existing Vitest + MSW + `renderWithQuery`/`AuthProvider` harness; mirror the file
layout of `web/src/workers/`. Assertions whose vacuity is the specific risk here:

**PATCH body (the highest-value test in this slice)**
- Change only the timezone, save, and assert the parsed request body has **no**
  `cron_expr` key. Paired positive: change the cron and assert `cron_expr` **is**
  present. Without the positive control this passes against a form that sends nothing.
- Save with nothing changed issues **zero** requests; paired positive that one change
  issues exactly one PATCH.

**Runs request**
- Assert on parsed `URLSearchParams`: `scheduled_job_id` present, `limit=20` present,
  `sort` **absent**. The absence assertion needs the presence ones beside it, since
  the real failure (`sort` leaking in from a copied `listJobs`) is a 400 the panel
  would otherwise render as a generic error.

**Poll does not clobber a dirty form**
- With fake timers, type a new cron, advance past 10s while MSW returns a *different*
  `cron_expr`, and assert the input still holds the typed value. Paired positive: a
  fresh mount does pick up the new server value, so the test cannot pass because the
  poll never fired. Instrument the MSW hit count and prove it moved.

**Owner omission (both directions)**
- `owner_email: ""` renders no owner line and leaks no empty label; `owner_email:
  "a@b.c"` renders it. A one-directional test here passes against a page that simply
  forgot the field.

**Overlap options**
- Exactly `skip` and `allow` render; `queue` is absent. Both directions.

**Next fire**
- After a successful cron save, the panel shows the `next_run_at` from the **PATCH
  response**, not the pre-edit value - the discriminating input is a response whose
  `next_run_at` differs from the one the initial GET returned.

**Auth / not-found**
- A 404 from `getSchedule` renders the not-found card with a `← Schedules` link and
  renders **no** Trigger form and **no** action bar. A 500 renders the retryable card
  and Retry issues exactly one more request.

**Delete**
- `Delete` opens `ConfirmDialog`; Cancel issues **no** request (paired positive:
  Confirm issues exactly one `DELETE`), success tolerates the empty 204 body and
  navigates to `/schedules`.

**Invalidation**
- `update`, `remove` and `runNow` invalidate the bare `['schedules']` prefix and the
  detail/runs queries actually refetch. Mount them via `renderHook` so an **active
  observer** exists: a `fetchQuery` seed leaves none, `refetchType: 'active'` never
  fires, and the assertion passes vacuously.

**List entry point**
- The NAME link and the `Edit` link both have `href="/schedules/{id}"`, and clicking
  `Edit` does not trigger `Run now` or the enable toggle.

**Regression gate on the shared module**
- `web/src/schedules/api.test.ts`, `useScheduleActions.test.tsx`,
  `SchedulesTable.test.tsx` and `SchedulesPage.test.tsx` must stay green. The
  `setScheduleEnabled` re-expression and the `useScheduleActions` additions must
  require **zero** edits to the first two files; an assertion needing adjustment is
  itself the finding.

Plan-supplied test bodies are guesses until run RED. Every absence assertion above
carries a required positive control in the representation the real failure would take.

## Acceptance criteria

1. `/schedules/:id` renders for any authenticated user; the server's owner-or-admin
   404 (`ownedScheduledJob`) is the only access control, and a 404 renders a
   not-found card with a `← Schedules` link and no edit or action controls.
2. The Schedules list reaches it two ways: the NAME cell and a new `Edit` control in
   ACTIONS, both `<Link>`s to `/schedules/{id}`; `Run now` and `Enable`/`Disable`
   still work unchanged.
3. The header shows name, an `ENABLED`/`PAUSED` pill, and `Run now`,
   `Enable`/`Disable`, `Delete`; the identity line shows created, updated, next fire
   and last run, and shows **no owner** while `owner_email` is empty.
4. The Trigger panel edits `cron_expr`, `timezone` and `overlap_policy` inline and
   `PATCH`es **only changed fields**; an unchanged Save issues no request. There is no
   client-side cron or timezone validation - the server's 400 message is displayed
   verbatim beside the form.
5. The overlap control offers exactly `skip` and `allow`.
6. A refetch landing while the form is dirty does not overwrite the user's input, and
   a settled PATCH never writes back into draft state.
7. The Job spec panel renders `job_spec` as read-only pretty-printed JSON with no Edit
   control and no YAML.
8. The Next fire panel shows exactly one entry, sourced from `next_run_at`, dimmed
   with a "paused" note when disabled, and updated from the PATCH response after a
   cron or timezone save. A code comment names the filed next-fires enabler.
9. The Recent runs panel lists the latest 20 jobs from
   `GET /v1/jobs?scheduled_job_id={id}&limit=20` - never sending `sort` - with
   `STARTED | DUR | STATUS | JOB ID | OWNER`, job ids linking to `/jobs/{id}`, a
   `latest N of <total>` footer, and an empty state when the schedule has never fired.
10. `Delete` is confirmed through `ConfirmDialog` (destructive) whose copy states that
    past jobs are kept but unlinked and that a run in flight is not cancelled; on 204
    the page navigates to `/schedules`.
11. Detail and runs queries poll at 10s; the relative countdown ticks via
    `useNow(1000)` and issues no request.
12. Query keys nest under `['schedules']` so the existing bare-prefix invalidations
    reach them; no existing mutation in `useScheduleActions` is modified.
13. `npm test` and the production build are green; changes are confined to
    `web/src/schedules/`, `web/src/jobs/api.ts` and `web/src/app/router.tsx`; no
    backend change; `web/dist` is reverted before the change set is assembled.
14. Three backlog items are **proposed** (not auto-filed) in Phase 6: next-fires
    preview, detail-endpoint `owner_email`, and the job-spec editor.

## Risks

- **The changed-fields-only PATCH is the one place a plausible implementation is
  silently wrong.** Sending the whole form is the obvious thing to write, it passes
  every naive test, and its only symptom is a schedule that quietly drifts later every
  time someone opens the editor. The paired-positive test in the Testing section is a
  requirement, not a nicety.
- **The `sort`-with-filter 400** is easy to reintroduce by copying `listJobs`, which
  sets `sort` by default (`web/src/jobs/api.ts:50-56`). Hence the dedicated function
  and the parsed-params assertion.
- **This slice touches three shared modules** (`schedules/api.ts`,
  `useScheduleActions.ts`, `SchedulesTable.tsx`) plus `jobs/api.ts`. A reviewer should
  confirm no existing exported signature or asserted URL changed and that the four
  existing schedules test files needed no edits.
- **`web/dist` is tracked but stale**; a frontend build dirties it and it must be
  reverted before the change set is assembled.
- **Scope creep toward a job-spec editor.** PATCH accepts `job_spec`, the textarea
  already exists in `NewJobPage`, and it will look like a small addition. It is a
  separate slice; keep the panel read-only.
