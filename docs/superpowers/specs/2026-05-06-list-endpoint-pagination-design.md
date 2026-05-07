---
title: List endpoint pagination
status: draft
created: 2026-05-06
---

# List endpoint pagination

## Context

A browser SPA frontend is being built against the Relay API. Today every `List*` query returns the full table — no `LIMIT`, no cursor, no max. This is fine while the system has dozens of jobs and a handful of workers, but the SPA needs to render lists incrementally and the API has no way to serve "first 50 newest jobs". Without pagination the SPA either OOMs the browser or papers over the problem with client-side trimming.

This spec adds cursor-based pagination across all six unbounded list endpoints, in one consistent contract, so the SPA and CLI never have to learn two pagination styles.

## Decisions

| # | Decision | Rationale |
|---|---|---|
| 1 | **Scope:** all six unbounded list endpoints — `/v1/jobs`, `/v1/workers`, `/v1/users`, `/v1/scheduled-jobs`, `/v1/agent-enrollments`, `/v1/reservations` | Contract is the expensive part to design; once it exists, applying to the remaining endpoints is mechanical. Avoids long-term tax of two pagination styles in the same API. |
| 2 | **Response shape:** wrapped JSON envelope `{"items":[...], "next_cursor":"...", "total":N}` | One JSON shape carries everything; clients parse one struct. Idiomatic for new JSON APIs. Breaking change for the CLI — CLI is updated in the same PR. |
| 3 | **Pagination model:** cursor over `(created_at DESC, id DESC)`. Opaque base64url-encoded JSON | Stable under concurrent inserts (Relay's job stream is continuously written). Constant cost at any depth. Forward-compatible cursor format. |
| 4 | **Total:** always included in the envelope. `COUNT(*)` per page | SPA needs "Showing 50 of 274" affordance. Relay's tables are small enough that COUNT is cheap; revisit if profiling shows pain. |
| 5 | **Defaults:** `limit` default 50, max 200. Out-of-range → 400 | Round numbers; 200 caps server cost without being so small that auto-paginate makes excessive round-trips. |
| 6 | **Filter expansion:** out of scope this round | Pagination contract change is itself non-trivial. Captured in `docs/backlog/idea-2026-05-06-list-endpoint-filters.md`. |
| 7 | **Custom sort:** out of scope this round | Cursor format is forward-compatible — a `"k":"<sort_field>"` field can be added later without breaking existing cursors. Captured in `docs/backlog/idea-2026-05-06-list-endpoint-sort.md`. |
| 8 | **Nested endpoints:** `/v1/jobs/{id}/tasks` and `/v1/workers/{id}/workspaces` stay un-paginated | Naturally bounded by their parent. Pagination would be churn for no benefit. |

## API contract

### Request

```
GET /v1/<resource>?limit=<int>&cursor=<opaque>
```

- **`limit`** — optional. Default 50, max 200. Out-of-range (`0`, `-3`, `201`, `abc`) → `400 invalid limit`.
- **`cursor`** — optional. Empty/absent means first page. Malformed → `400 invalid cursor` (response body must not echo decoded bytes).
- **Existing filters preserved** — `?status=`, `?scheduled_job_id=` on `/v1/jobs`; `?email=`, `?include_archived=` on `/v1/users`.

### Response (200 OK)

```json
{
  "items": [ ... ],
  "next_cursor": "eyJ0IjoiMjAyNi0wNS0wNlQxNToz...",
  "total": 274
}
```

- **`items`** — array of resource objects. Per-row representation unchanged from today (only the wrapper is new).
- **`next_cursor`** — `""` (empty string) when this is the last page; non-empty otherwise. Empty result set yields `""`, never echoes the input cursor.
- **`total`** — total row count, honoring the same filters as the listing query.

### Sort order

`created_at DESC, id DESC` for every paginated endpoint. The `id` tiebreaker matters: two rows can share a `created_at` to microsecond precision, and without the tiebreaker the cursor's strict `<` comparison can skip rows.

### Cursor format

`base64url(no padding)` of JSON `{"t":"<RFC3339 µs>","i":"<uuid>"}`.

Documented as **opaque** in the README — clients must not parse it. The format may change in future releases. Self-describing internally for debuggability (a developer can base64-decode in a terminal).

### Behavior changes (user-visible)

1. **`/v1/workers`** sort order changes from `name ASC` to `created_at DESC, id DESC`.
2. **`/v1/users`** sort order changes from `created_at ASC` to `created_at DESC, id DESC`.
3. **`/v1/users?email=<exact>`** still returns at most one row but is wrapped in the envelope (`{"items":[u], "next_cursor":"", "total":1}` or `{"items":[], ..., "total":0}`) for shape uniformity.

Each is called out in the README and the PR description.

### Endpoints

| Endpoint | Filters preserved |
|---|---|
| `/v1/jobs` | `?status=`, `?scheduled_job_id=` |
| `/v1/workers` | (none) |
| `/v1/users` | `?email=`, `?include_archived=` |
| `/v1/scheduled-jobs` | (admin sees all, owner sees own) |
| `/v1/agent-enrollments` | (active only — `consumed_at IS NULL AND expires_at > NOW()`) |
| `/v1/reservations` | (none) |

## Implementation core

### `internal/api/pagination.go` (new)

Three pieces of reusable infrastructure:

**Cursor codec.**
```go
type cursor struct {
    Set bool        // false = first page
    T   time.Time   // last-seen created_at (truncated to microsecond)
    ID  pgtype.UUID // last-seen row id (tiebreaker)
}

func encodeCursor(t time.Time, id pgtype.UUID) string
func decodeCursor(s string) (cursor, error)  // "" → Set=false, no error
```

`encodeCursor` MUST truncate `t` to `time.Microsecond` before serializing. Postgres `timestamptz` is microsecond-precision; Go `time.Time` is nanosecond. Without truncation, a strict `<` comparison can skip the row at the cursor boundary.

`decodeCursor` returns sentinel `errBadCursor` for any decode failure. Handlers translate to `400 invalid cursor` and **do not** echo decoded bytes.

**Request parser.**
```go
type pageParams struct {
    Limit  int32  // validated, in [1, 200]
    Cursor cursor
}

func parsePage(w http.ResponseWriter, r *http.Request) (pageParams, bool)
```

Writes the 400 response itself on `?limit=` or `?cursor=` errors and returns `ok=false`.

**Response envelope + page builder.**
```go
type page[T any] struct {
    Items      []T    `json:"items"`
    NextCursor string `json:"next_cursor"`
    Total      int64  `json:"total"`
}

func buildPage[Row, Out any](
    rows []Row,
    limit int32,
    conv func(Row) Out,
    key func(Row) (time.Time, pgtype.UUID),
) (items []Out, nextCursor string)
```

`buildPage` consumes `limit+1` fetched rows (the "+1 trick" for "more available" detection without a second query), trims to `limit`, and emits the cursor pointing at the **last kept row**'s key — never the trimmed extra row's key. Fewer than `limit+1` rows → empty cursor. Empty input → empty items, empty cursor.

### Handler pattern

Every paginated handler follows this shape (jobs default branch shown):

```go
func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()

    pp, ok := parsePage(w, r)
    if !ok { return }

    // ... filter branches (status, scheduled_job_id) call their own paginated query

    rows, err := s.q.ListJobsWithEmailPage(ctx, store.ListJobsWithEmailPageParams{
        CursorSet: pp.Cursor.Set,
        CursorTs:  pp.CursorTs(),
        CursorId:  pp.Cursor.ID,
        PageLimit: pp.Limit,
    })
    if err != nil { writeError(w, 500, "list jobs failed"); return }

    total, err := s.q.CountJobs(ctx)
    if err != nil { writeError(w, 500, "count jobs failed"); return }

    items, next := buildPage(rows, pp.Limit, toJobResponseFromRow, jobsRowKey)
    writeJSON(w, 200, page[jobResponse]{Items: items, NextCursor: next, Total: total})
}
```

### Filter-branch handling

- **`/v1/jobs`** has two filter branches (`?status=`, `?scheduled_job_id=`); each gets its own paginated query + matching count.
- The auth gate (`s.ownedScheduledJob` for the `?scheduled_job_id=` branch) MUST run before pagination. Pagination cannot bypass authorization.
- **`/v1/users?email=<exact>`** returns at most one row; wraps the result in the envelope for shape uniformity.
- **`/v1/scheduled-jobs`** already splits admin (sees all) vs non-admin (sees own); both branches paginate against their respective queries.

## Database

### Query changes — `internal/store/query/*.sql`

Per file, add paginated `List*Page` queries plus matching `Count*`. For each kept legacy query, the implementation plan must verify it has a real (non-API) caller before keeping it; un-referenced legacy queries are deleted.

**`jobs.sql`**
- Add: `ListJobsWithEmailPage`, `CountJobs`, `ListJobsByStatusWithEmailPage`, `CountJobsByStatus`, `ListJobsByScheduledJobWithEmailPage`, `CountJobsByScheduledJob`.
- Keep: `ListJobsByScheduledJob` (used by schedrunner internals).
- Verify-and-likely-delete: `ListJobs`, `ListJobsByStatus`, `ListJobsWithEmail`, `ListJobsByStatusWithEmail` (no API callers after migration).

**`workers.sql`**
- Add: `ListWorkersPage`, `CountWorkers`.
- Keep: `ListWorkers` (used by `internal/scheduler/dispatch.go`).

**`users.sql`**
- Add: `ListUsersPage`, `CountUsers`, `ListUsersIncludingArchivedPage`, `CountUsersIncludingArchived`.
- Verify-and-likely-delete: legacy `ListUsers`, `ListUsersIncludingArchived` after handler migration.

**`scheduled_jobs.sql`**
- Add: `ListScheduledJobsPage`, `CountScheduledJobs`, `ListScheduledJobsByOwnerPage`, `CountScheduledJobsByOwner`.
- Verify-and-likely-delete: legacy `ListScheduledJobs`, `ListScheduledJobsByOwner`.

**`agent_enrollments.sql`**
- Add: `ListActiveAgentEnrollmentsPage`, `CountActiveAgentEnrollments`. Predicate `consumed_at IS NULL AND expires_at > NOW()` stays.
- Keep: `ListActiveAgentEnrollments` (used by store-layer test).

**`reservations.sql`**
- Add: `ListReservationsPage`, `CountReservations`.
- Keep: `ListActiveReservations` (used by scheduler).
- Verify-and-likely-delete: legacy `ListReservations`.

### Single bind shape (every paginated query)

```sql
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (created_at, id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int + 1
```

The `+ 1` lives in SQL. `cursor_set=FALSE` short-circuits the predicate on the first page.

After SQL edits, run `make generate`. Never hand-edit `*.sql.go` or `models.go`.

### New migration: `internal/store/migrations/000011_pagination_indexes.{up,down}.sql`

```sql
-- up.sql
CREATE INDEX idx_jobs_created_id          ON jobs(created_at DESC, id DESC);
CREATE INDEX idx_jobs_status_created_id   ON jobs(status, created_at DESC, id DESC);
CREATE INDEX idx_jobs_sched_created_id    ON jobs(scheduled_job_id, created_at DESC, id DESC) WHERE scheduled_job_id IS NOT NULL;
CREATE INDEX idx_workers_created_id       ON workers(created_at DESC, id DESC);
CREATE INDEX idx_users_created_id         ON users(created_at DESC, id DESC);
CREATE INDEX idx_sched_jobs_created_id    ON scheduled_jobs(created_at DESC, id DESC);
CREATE INDEX idx_sched_jobs_owner_created ON scheduled_jobs(owner_id, created_at DESC, id DESC);
CREATE INDEX idx_agent_enr_created_id     ON agent_enrollments(created_at DESC, id DESC) WHERE consumed_at IS NULL;
CREATE INDEX idx_reservations_created_id  ON reservations(created_at DESC, id DESC);

-- Drop single-column indexes that are now prefixes of new composites.
-- Names below are best-guesses; the implementation plan must audit migration
-- 000001_initial.up.sql and any subsequent migrations to confirm exact names
-- before finalizing this drop set. Wrong names are silent (IF EXISTS) but
-- leave dead indexes behind.
DROP INDEX IF EXISTS idx_jobs_status;
DROP INDEX IF EXISTS idx_jobs_scheduled_job_id;
DROP INDEX IF EXISTS idx_scheduled_jobs_owner;
```

`down.sql` reverses both directions: drop the composites, recreate the single-column indexes.

**Why no `CONCURRENTLY`:** golang-migrate's embedded migrator runs each file in a transaction. `CREATE INDEX CONCURRENTLY` cannot run inside a transaction. Tables are small enough today (well under 100k rows) that the brief lock during `CREATE INDEX` is acceptable. If the schema grows, a separate concurrent-index migration outside the embedded migrator is the follow-up — not a blocker for this PR.

## CLI

### `internal/cli/page.go` (new)

```go
type pageEnvelope[T any] struct {
    Items      []T    `json:"items"`
    NextCursor string `json:"next_cursor"`
    Total      int64  `json:"total"`
}

const pageRequestLimit = 200

// fetchAllPages walks next_cursor until exhausted (or until userLimit rows
// are collected, if non-zero). Returns the combined slice and the total
// reported by the first page.
func fetchAllPages[T any](
    ctx context.Context,
    c *Client,
    basePath string,
    params url.Values,
    userLimit int,        // 0 = walk all pages
) ([]T, int64, error)
```

Requests `?limit=200` per call to minimize round-trips. Filters (`status=`, `include_archived=`, etc.) flow through `params` unchanged.

### List commands updated

| Command | File |
|---|---|
| `relay jobs list` | `internal/cli/jobs.go` |
| `relay workers list` | `internal/cli/workers.go` |
| `relay schedules list` | `internal/cli/schedules.go` |
| `relay reservations list` | `internal/cli/reservations.go` |
| `relay admin users list` | `internal/cli/admin_users.go` |
| `relay admin enroll list` (or equivalent) | `internal/cli/agent_enroll.go` |

Each command:
1. **Default behavior unchanged** — prints all rows; CLI auto-walks pages internally.
2. **New `--limit N` flag** — caps output at N rows; passed as `userLimit` to `fetchAllPages`.
3. **`Total: N` header line** above the table when not in `--json` mode.

### Email-lookup branches in admin users

Three call sites currently expect a bare `[]userListItem` and must unwrap `pageEnvelope[userListItem].Items`:
- `doAdminUsersGet`
- `doAdminUsersUpdate`
- `doAdminUsersArchiveAction`

### Hostname → ID resolution in `relay workers`

`internal/cli/workers.go`'s `resolveWorkerID` helper currently calls `c.do` directly and unmarshals a bare array. Switches to `fetchAllPages[workerResp]` so it works correctly with >200 workers and goes through the same pagination machinery as everything else.

### Why CLI default stays "walk all pages"

1. Backward compat for `relay jobs list | grep ...` shell scripts.
2. CLI users read tables, not response bodies — silent truncation would confuse. `Total:` header still informs the user when `--limit` truncates.

## Testing

### Unit tests — `internal/api/pagination_test.go` (new, no build tag)

- `TestCursor_RoundTrip`
- `TestCursor_TruncatesNanos` (boundary-skip defense)
- `TestCursor_Empty` (yields `Set=false`, no error)
- `TestCursor_InvalidBase64` (returns `errBadCursor`)
- `TestCursor_InvalidJSON` (valid base64, non-JSON contents)
- `TestParsePage_Defaults`
- `TestParsePage_LimitClamping` (table-driven: valid mid, max, zero, negative, over-max, non-numeric)
- `TestParsePage_BadCursor`
- `TestBuildPage_NoMore`
- `TestBuildPage_HasMore` (cursor encodes last-kept row, not trimmed extra)
- `TestBuildPage_EmptyResult`

### Integration tests — `internal/api/jobs_pagination_test.go` (new, `//go:build integration`)

`/v1/jobs` is the most complex handler; deepest coverage lives here.

- `TestListJobs_PaginationDefaultLimit` — 75 jobs, page 1 has 50+cursor+total=75, page 2 has 25+empty cursor+total=75, no duplicate IDs.
- `TestListJobs_StableUnderInsertMidPage` — fetch page 1, insert "interloper", fetch page 2 with saved cursor → interloper not present.
- `TestListJobs_LimitParam` — `?limit=3` over 5 jobs.
- `TestListJobs_LimitOutOfRange` — `0`, `201`, `-3`, `abc` all → 400.
- `TestListJobs_BadCursor` — body contains `invalid cursor`, no decoded bytes.
- `TestListJobs_EmptyResult`.
- `TestListJobs_StatusFilterPaginated` — filter + count integrity.

### Per-endpoint smoke tests

The other five handlers (workers, users, scheduled-jobs, agent-enrollments, reservations) get basic happy-path tests added inline to existing `*_test.go` files: envelope shape, `total` field present, cursor walks correctly. The cursor codec and `buildPage` are covered by unit tests, so we don't repeat the full matrix per endpoint.

### Existing-test impact

Roughly 15–25 sites across `internal/api/*_test.go` and `internal/cli/*_test.go` decode list responses as bare arrays. All need an envelope unwrap. Two specific behavior-driven fixes:
1. `TestListUsers_OrderedByCreatedAt` — expected order flips ASC → DESC.
2. CLI tests asserting `assert.Empty(t, r.URL.RawQuery)` — auto-paginate appends `?limit=200`, so assertions tighten to `assert.Equal(t, "limit=200", ...)` or `assert.Contains` for filter+limit cases.

## Documentation

### `README.md`

New "**Pagination**" subsection under API Reference:
- Wire shape with example response.
- `?limit=` and `?cursor=` query params, defaults (50/200).
- Sort order: `created_at DESC, id DESC` for all paginated endpoints.
- **Opaque cursor contract:** "Clients must treat `next_cursor` as opaque. Its format is server-internal and may change without notice."
- `total` semantics (honors filters).

API table updated: each of the six list endpoints annotated as **Paginated**. The two ordering changes (workers, users) called out explicitly.

### Backlog entries

**`docs/backlog/idea-2026-05-06-list-endpoint-filters.md`** — deferred filter expansion:
- Multi-value `?status=running,queued` on `/v1/jobs`
- `?submitted_by=<user_id_or_email>` on `/v1/jobs`
- `?since=<ts>` / `?until=<ts>` time-range filters on `/v1/jobs`
- `?enabled=true` on `/v1/scheduled-jobs`
- `?status=online|offline` on `/v1/workers`
- Label filtering via JSONB containment (`?label.team=infra`) — needs GIN index
- Substring name search (`?q=`) — needs `pg_trgm` index
- Note: filters that change ordering need a different cursor scheme.

**`docs/backlog/idea-2026-05-06-list-endpoint-sort.md`** — deferred custom sort:
- Per-endpoint sort whitelist (e.g., `?sort=name|status&order=asc`).
- Cursor codec extension: `{"k":"<sort_field>","v":"<value>","i":"<uuid>"}`.
- One composite index per sortable field.
- Forward-compatible with current cursor format — old cursors decode cleanly into the default `(created_at, id)` interpretation.

## Verification gates

Pre-merge:
1. `make generate` after sqlc edits — confirm no `*.sql.go` hand-edits.
2. `make build` — all three binaries.
3. `make test` — unit tests pass.
4. `make test-integration` — Docker-backed integration tests pass.

End-to-end smoke:
1. Start `relay-server` against local Postgres, seed 75 jobs.
2. `curl 'http://localhost:8080/v1/jobs'` → 50 items + cursor + total=75.
3. `curl '...?cursor=<that>'` → 25 items + empty cursor + total=75.
4. `relay jobs list` → 75 rows + `Total: 75` header.
5. `relay jobs list --limit 10` → 10 rows + header.

## Risks and edge cases

1. **Microsecond precision.** Go `time.Time` (nanosecond) round-tripping through Postgres `timestamptz` (microsecond). `encodeCursor` truncates to microsecond; verify in unit tests with rows whose `created_at` differs by <1µs.
2. **Mixed-version deploys.** New CLI against old server returns a bare array; `json.Unmarshal` into `pageEnvelope[T]` fails. Mitigation: server and CLI ship together from the same repo. Coordinated release.
3. **Existing test fixtures.** ~15–25 test sites unmarshal bare arrays. All need envelope unwrapping. Plan must enumerate each.
4. **Workers and users ordering changes** are visible to humans running `relay workers list` and `relay admin users list`. Called out in PR description and README. Not a blocker.
5. **Empty-result cursor.** Trimmed `items` is empty → `next_cursor` MUST be `""` (don't echo input cursor). Tested explicitly.
6. **Ownership before pagination.** The `?scheduled_job_id=` ownership gate via `ownedScheduledJob` returns 404 for non-owners; verify pagination doesn't bypass it (the gate runs on the parent ID, not the listed rows).
7. **Index audit.** Existing index names from migration 000001 may not match the `DROP INDEX IF EXISTS` set in the migration. Implementation plan must verify before finalizing.
8. **Legacy query callers.** Each "verify and likely delete" query in the database section needs caller verification before deletion lands in the PR.

## Critical files

- `internal/api/pagination.go` — **new**: cursor codec, `parsePage`, `page[T]`, `buildPage`
- `internal/api/jobs.go` — most complex handler (two filter branches + ownership gate)
- `internal/api/workers.go`, `users.go`, `scheduled_jobs.go`, `reservations.go`, `agent_enrollments.go`
- `internal/store/query/jobs.sql` — six new sqlc queries
- `internal/store/query/workers.sql`, `users.sql`, `scheduled_jobs.sql`, `agent_enrollments.sql`, `reservations.sql`
- `internal/store/migrations/000011_pagination_indexes.up.sql` / `.down.sql` — **new**
- `internal/cli/page.go` — **new**: `fetchAllPages` helper
- `internal/cli/jobs.go`, `workers.go`, `schedules.go`, `reservations.go`, `agent_enroll.go`, `admin_users.go`
- `README.md` — API reference + Pagination section
- `docs/backlog/idea-2026-05-06-list-endpoint-filters.md` — **new**
- `docs/backlog/idea-2026-05-06-list-endpoint-sort.md` — **new**
