# Web Schedules List Page - Design

Date: 2026-06-05
Status: Approved

## Summary

Build the Schedules list page of the Relay web front end, mounted at `/schedules`
(replacing the current `JobsPlaceholder`). It recreates the `HoloSchedules`
design from the Holo handoff against the real `GET /v1/scheduled-jobs` endpoint,
with two inline mutations (run-now and enable/disable). It mirrors the shipped
Workers slice (`web/src/workers/`) in module shape, polling, and state handling.

One small backend addition is in scope: an `owner_email` field on the scheduled
job list response, resolved via a batch user lookup.

Out of scope this slice: the schedule detail page (the design's "Edit" target),
filter chips, free-text search, and the design's "FAILED 24h" summary stat.

## Decisions

These were settled during brainstorming:

- **Scope: the list page**, recreating `HoloSchedules` as far as the backend
  allows. Detail page deferred to its own slice.
- **Row actions: Run now + enable/disable** (both endpoints already exist).
  No "Edit" button, because the schedule-detail page does not exist yet.
- **Data gaps: add `owner_email` to the list response**; degrade the rest.
  The design's last-job status dot and "FAILED 24h" stat have no backend data
  source, so the last job shows as a plain id and the failed stat is dropped.
- **Filtering: deferred.** The list endpoint supports only sort + cursor
  pagination, no server-side `enabled`/text filtering. Ship sort + pagination;
  skip the chips and search box this slice.
- **Run now stays visible on paused schedules** (the backend allows running a
  disabled schedule; hiding the action would remove a valid operation). The
  prototype only showed it when enabled - we intentionally diverge.
- **Polling at 10s** (schedules are low-churn vs. workers' 3s). The relative
  "next run" countdown is derived client-side from the absolute `next_run_at`,
  so a fast server poll is unnecessary; a lightweight local re-render ticks the
  relative times between polls.
- **Sort via a dropdown** (the prototype's `SortControl`), not Workers' clickable
  headers - most schedule columns are not sortable and "Recently run"
  (`updated_at`) has no column to click.

## Backend changes

### `owner_email` on the list response

`scheduledJobResponse` (in `internal/api/scheduled_jobs.go`) gains:

```go
OwnerEmail string `json:"owner_email"`
```

Resolution strategy - **batch lookup, not query JOIN**. The 16 existing
paginated list query variants (8 admin + 8 owner-scoped) are left untouched;
they still return `store.ScheduledJob`. After `handleListScheduledJobs` assembles
the page items:

- **Non-admin path:** every row is owned by the caller, so set each item's
  `owner_email` to `u.Email` directly. No lookup.
- **Admin path:** collect the distinct `owner_id`s from the page, run one new
  query to resolve emails, and map them onto the items.

New sqlc query in `internal/store/query/users.sql` (or the appropriate users
query file):

```sql
-- name: GetUserEmailsByIDs :many
SELECT id, email FROM users WHERE id = ANY($1::uuid[]);
```

Run `make generate` after the `.sql` edit. Do not hand-edit `*.sql.go` /
`models.go`.

Rationale: a JOIN would change every generated row type and ripple through
`buildPage` and the row-key helpers, risking regressions in the cursor logic for
one display field. The batch lookup is surgical.

### Backend test

Integration test (`//go:build integration`) asserting `owner_email` is populated:

- Admin view with schedules owned by two different users -> each row carries the
  correct owner email.
- Owner-scoped (non-admin) view -> the caller's email on their own rows.

## Frontend module

New `web/src/schedules/`, mirroring `web/src/workers/`:

| File | Purpose |
| --- | --- |
| `api.ts` | `Schedule`, `SchedulesPage`, `ScheduleSort` types; `listSchedules(sort, cursor)`, `runScheduleNow(id)`, `setScheduleEnabled(id, enabled)`. |
| `useSchedules.ts` | TanStack Query polling hook, keyed by `['schedules', sort, cursor]`, `placeholderData: keepPreviousData`, `refetchInterval: 10000`. |
| `useScheduleActions.ts` | `useMutation`s for run-now and enable/disable; invalidate `['schedules']` on success. |
| `format.ts` | Pure helpers: relative-time, next-run display, `shortId`. |
| `SchedulesTable.tsx` | Table header + rows + row actions. |
| `SchedulesPage.tsx` | Composition: header/eyebrow, summary strip, sort dropdown, table, footer/pagination, loading/error/empty states. |

`web/src/app/router.tsx`: mount `<SchedulesPage />` at `/schedules`.

### TypeScript contract

`Schedule` matches Go `scheduledJobResponse` field-for-field (verified in
review):

```ts
export interface Schedule {
  id: string
  name: string
  owner_id: string
  owner_email: string
  cron_expr: string
  timezone: string
  job_spec: unknown        // raw JSON; not rendered in the list
  overlap_policy: string   // 'skip' | 'allow'
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
  | '-created_at' | 'created_at'
  | 'name' | '-name'
  | 'next_run_at' | '-next_run_at'
  | 'updated_at' | '-updated_at'
```

`listSchedules` passes `limit=50` explicitly and the `cursor` when present.

### Page layout

- **Header:** eyebrow `RECURRING` + H1 `Schedules`.
- **Summary strip (page-scoped, labeled):** `ENABLED` (ok) / `PAUSED` (fg) counts
  from the loaded page, plus `N schedules` from the backend `total`. The
  "FAILED 24h" stat from the design is omitted.
- **Sort dropdown:** options mapping exactly to backend sort keys -
  Newest (`-created_at`), Oldest (`created_at`), Name A->Z (`name`),
  Name Z->A (`-name`), Next run soonest (`next_run_at`), Next run latest
  (`-next_run_at`), Recently run (`-updated_at`), Least recently run
  (`updated_at`). Default `-created_at`.
- **Table columns (8):** NAME (enabled dot + name) - CRON (mono) - TZ - OVERLAP
  (chip, accent if `allow`) - NEXT RUN (relative; `▸` marker when enabled) -
  LAST RUN (relative, or `-`) - LAST JOB (short `last_job_id`, plain mono, `-`
  if none; no link/dot yet) - OWNER (`owner_email`).
- **Row actions (right-aligned):** "Run now" always; plus "Disable" when enabled
  or "Enable" when disabled. Each button disables while its mutation is pending.
- **Disabled rows:** `opacity-0.55`.
- **Footer:** `SHOWING 1-N OF {total}` + cursor pagination (see below).

### Pagination

Cursor-based with a client-side cursor stack:

- "Next 50 ->" pushes the current `next_cursor` onto the stack and refetches.
- "<- prev" pops the stack and refetches the prior cursor.
- Next is disabled when `next_cursor` is empty; prev is disabled at the base.

### Polling and live relative times

`refetchInterval: 10000`. The relative "next run" / "last run" strings are
computed client-side from absolute timestamps, so a low-frequency local timer
re-renders the page to keep the countdowns fresh between polls without extra
network traffic.

## Error handling

Mirror `WorkersPage`:

- Loading skeleton (row placeholders) on first load.
- Error card with a Retry button when the initial fetch fails.
- Empty state: "No schedules yet."
- Mutation errors surfaced inline (no toast system exists); a small inline error
  near the header is consistent with current patterns.

## Testing

Frontend:

- `api.test.ts` - URL/query construction for list, run-now, enable/disable.
- `useSchedules` - polling and `keepPreviousData` behavior.
- `format.ts` - relative-time, next-run, `shortId` helpers.
- `SchedulesTable` - columns render; action buttons match enabled-state;
  pending state disables buttons.
- `SchedulesPage` - loading/error/empty states; sort change; pagination
  next/prev.

Backend:

- Integration test for `owner_email` population (admin multi-owner +
  owner-scoped).

Contract: TS `Schedule` checked field-for-field against Go
`scheduledJobResponse`.

## Follow-up backlog

- Schedule detail page + the "Edit" action.
- Last-job link + status dot (needs Jobs detail page + last-job status).
- "FAILED 24h" summary stat (needs a failed-runs aggregate).
- Server-side `enabled` filter + name search (enables the design's chips +
  search box).
