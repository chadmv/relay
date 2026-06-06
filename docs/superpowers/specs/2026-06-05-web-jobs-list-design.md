# Web Jobs List page (Table view) - Design

Date: 2026-06-05

## Summary

Implement the Jobs list page for the Relay web frontend, replacing the current
`JobsPlaceholder` on the `/jobs` route. This first slice ships the **Table view**
only (the design's marked default), backed by an **enriched** `GET /v1/jobs` and a
**new** `GET /v1/jobs/stats`. The page mirrors the structure and visual language of
the existing `web/src/workers/` feature.

Authoritative design reference: `HoloJobsList` in
`design_handoff_relay_holo/hifi3-holo-pages.jsx` (the picked "Holo" direction).

## Scope decisions

Settled during brainstorming:

- **Views:** Table only. Lanes and Timeline deferred to backlog.
- **Columns:** Full enrichment to match the mock - ID, Name (+ schedule chip),
  Status, Progress, Started, Duration, Owner.
- **KPI strip:** Sourced from a new `GET /v1/jobs/stats` aggregate endpoint
  (mirrors the existing `/v1/workers/stats` precedent).
- **My jobs toggle + search box:** Deferred to backlog. Both are client-side-only
  in the mock and misleading under server pagination; ship server-backed status
  chips + sort only.
- **Pagination:** Real next / prev via a client-side cursor stack (server returns
  only `next_cursor`; prev is maintained client-side).
- **Row click -> job detail:** Deferred. The detail page is a separate future
  slice, so rows are not clickable in v1 and the chevron affordance is dropped.

## Backend

### 1. Enrich the jobs list

`GET /v1/jobs` list rows currently return `j.*` + `submitted_by_email`. They lack
the data the table needs: task progress, timing, and schedule source.

Add the same per-row enrichment to all **12 paginated list queries** in
`internal/store/query/jobs.sql` (the 10 sort variants: default `-created_at`,
`created_at`, `name`/`-name`, `priority`/`-priority`, `status`/`-status`,
`updated_at`/`-updated_at`; plus `ListJobsByStatusWithEmailPage` and
`ListJobsByScheduledJobWithEmailPage`):

```sql
LEFT JOIN LATERAL (
  SELECT COUNT(*)                                  AS total_tasks,
         COUNT(*) FILTER (WHERE t.status = 'done') AS done_tasks,
         MIN(t.started_at)                         AS started_at,
         MAX(t.finished_at)                        AS finished_at
  FROM tasks t WHERE t.job_id = j.id
) ts ON TRUE
LEFT JOIN scheduled_jobs sj ON sj.id = j.scheduled_job_id
```

The `LEFT JOIN LATERAL` runs once per returned job row (at most `page_limit`
rows), so it does not scan the full tasks table. `scheduled_job_id` already exists
on `jobs` (migration `000006`); the join to `scheduled_jobs` resolves its name for
the chip.

The cursor/sort `ORDER BY` and `WHERE` clauses are unchanged - the enrichment only
adds joined columns to the `SELECT`. Operator-precedence care is not needed because
no new `WHERE` conditions are introduced.

New fields on `jobResponse` in `internal/api/jobs.go`, all `omitempty`:

| Field | Source | Type |
| --- | --- | --- |
| `total_tasks` | `ts.total_tasks` | int |
| `done_tasks` | `ts.done_tasks` | int |
| `started_at` | `ts.started_at` (nullable) | RFC3339 string |
| `finished_at` | `ts.finished_at` (nullable) | RFC3339 string |
| `scheduled_job_id` | `j.scheduled_job_id` (nullable) | string |
| `scheduled_job_name` | `sj.name` (nullable) | string |

The 12 `jobsRowKey*` and `jobRowToResponse*` helpers are updated for the new
generated row types. Sort-key extraction (`jobsRowKey*`) is unchanged since the
sort columns (`created_at`, `name`, `priority`, `status`, `updated_at`, `id`) are
still present. The single-job `GetJobWithEmail` / `handleGetJob` path is left
unchanged - it already returns full task detail, from which a detail page could
derive progress itself.

Progress % (`done/total`) and duration (`(finished ?? now) - started`) are derived
client-side; the backend returns raw counts and timestamps.

### 2. Jobs stats endpoint

New `GET /v1/jobs/stats` (auth required, not admin-only), modeled on
`handleWorkerStats`. Backed by one aggregate query:

```sql
-- name: JobStatusCounts :one
SELECT
  COUNT(*) FILTER (WHERE status IN ('running','dispatched'))                                              AS running,
  COUNT(*) FILTER (WHERE status IN ('queued','pending'))                                                  AS queued,
  COUNT(*) FILTER (WHERE status = 'done'                  AND updated_at >= NOW() - INTERVAL '24 hours')  AS done_24h,
  COUNT(*) FILTER (WHERE status IN ('failed','timed_out') AND updated_at >= NOW() - INTERVAL '24 hours')  AS failed_24h
FROM jobs;
```

Response shape:

```json
{ "running": 3, "queued": 1, "done_24h": 487, "failed_24h": 12 }
```

Registered in `internal/api/server.go`. The `done_24h` / `failed_24h` buckets use
`updated_at` as the terminal-transition proxy (jobs have no dedicated
`finished_at` column).

### 3. Status semantics

Job status set: `pending`, `queued`, `dispatched`, `running`, `done`, `failed`,
`timed_out`, `cancelled`. Color mapping (follows the hi-fi `HoloJobsList`, which is
the picked direction):

| Status | Color token |
| --- | --- |
| `done` | `ok` |
| `running`, `dispatched` | `accent` |
| `queued`, `pending` | `warn` |
| `failed`, `timed_out` | `err` |
| `cancelled`, other | `fg-mute` |

Status filter chips map to a single server `?status=` value each:
All (no filter), Running, Queued, Done, Failed.

## Frontend

New feature directory `web/src/jobs/`, mirroring `web/src/workers/`:

| File | Role |
| --- | --- |
| `api.ts` | `Job`, `JobStats`, `JobsPage`, `JobSort`, `JobStatus` types; `listJobs(sort, status, cursor?)`, `getJobStats()` |
| `useJobs.ts` | react-query polling hook (3s, `keepPreviousData`), keyed by `[sort, status, cursor]` |
| `useJobStats.ts` | react-query polling hook for the KPI strip |
| `status.ts` | status -> color map; `formatDuration`, `formatStarted` helpers |
| `JobsTable.tsx` | table rows (presentational; no header-click sorting) |
| `JobsPage.tsx` | KPI strip, filter chips, sort control dropdown, table, pagination footer |

Plus Vitest coverage per file, matching the Workers test set
(`api.test.ts`, `useJobs.test.tsx`, `useJobStats.test.tsx`, `JobsTable.test.tsx`,
`JobsPage.test.tsx`). Route wiring: replace `JobsPlaceholder` with `JobsPage` for
`/jobs` in `web/src/app/router.tsx`.

### Table columns

`ID | Name (+ schedule chip) | Status (dot + text) | Progress (bar + %) | Started | Dur | Owner`

- **Name** truncates with ellipsis; the schedule chip (`⟳ <scheduled_job_name>`)
  renders only when `scheduled_job_id` is present.
- **Progress** is a thin bar filled to `done/total`, with the `%` to its right.
  Fill color: `ok` for done jobs, `err` for failed, accent gradient otherwise.
- **Started** shows `started_at` formatted compactly; `-` when null.
- **Dur** shows `(finished ?? now) - started`; `-` when not started.
- **Owner** shows `submitted_by_email`.

### KPI strip

Eyebrow + counts from `getJobStats`, with a page-scoped fallback until the first
stats response arrives (same pattern as `WorkersPage`):
`<n> RUNNING · <n> QUEUED · <n> DONE·24H · <n> FAILED·24H`. Colors per the status
map (running=accent, queued=warn, done=ok, failed=err).

### Sort control

A dropdown sort control in the toolbar (per the mock - not header-click sorting,
since options like Priority and Recently-updated have no visible column). Sort
options:
Newest/Oldest (`created_at`), Name A->Z / Z->A, Priority high->low / low->high,
Status A->Z / Z->A, Recently updated / Least recently updated.

The sort control is **disabled while a status filter chip is active**, because the
server rejects `?sort=` combined with `?status=` (`400 sort not supported on
filtered list variant`). Selecting a status chip snaps sort back to the default
`-created_at`. This matches the mock's behavior exactly.

### Pagination

Real next / prev. The server returns only `next_cursor`, so prev is implemented by
maintaining a client-side stack of cursors. Footer:
`SHOWING 1-N OF <total> · SORT <sort> · CURSOR PAGINATED` with prev/next pill
buttons (prev disabled on the first page; next disabled when `next_cursor` is
empty). `total` comes from the list envelope (already returned by the backend).

### Polling / live indicator

3s poll via react-query with `keepPreviousData` so the table never flashes empty
between polls or sort changes. Header shows the same `live · auto-refreshing`
indicator as the Workers page, pulsing on `isFetching`.

## Deferred to backlog

- **Lanes view** (per-status swimlanes, capped per-lane).
- **Timeline view** (6h/24h/7d windowed; needs a time-window query the backend
  lacks).
- **My jobs toggle** (`?mine=`) and **search box** (`?q=`) - need real server-side
  filters to be correct under pagination.
- **Job detail page** and row-click navigation.

## Verification

- `make test` and `make test-integration` pass.
- New integration tests: list enrichment (task counts, timing, schedule name);
  stats buckets and the 24h window.
- Frontend Vitest suite passes (`web/` npm test).
- Manual: `/jobs` renders the table with progress bars, KPI strip reflects
  fleet-wide counts, status chips filter, sort works when unfiltered and is
  disabled when filtered, next/prev paginate correctly.
