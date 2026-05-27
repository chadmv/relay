# Configurable Sort Order for List Endpoints — Design

**Date:** 2026-05-26
**Status:** Approved, ready for implementation plan
**Backlog item:** [idea-2026-05-06-list-endpoint-sort](../../backlog/idea-2026-05-06-list-endpoint-sort.md)

## Problem

All six paginated REST list endpoints are hardcoded to `ORDER BY created_at DESC, id DESC`. This is the right default for time-ordered lists, but several plausible use-cases want different orderings:

- `GET /v1/jobs?sort=status` for a kanban-style view.
- `GET /v1/workers?sort=name` for the pre-pagination alphabetical order.
- `GET /v1/jobs?sort=-priority` to surface high-priority jobs first.

The cursor scheme is the core constraint: a cursor encoding `(created_at, id)` is only valid for `ORDER BY created_at DESC, id DESC`. Any new sort order needs its own cursor semantics and a server-side check that the cursor's sort key matches the request.

## Goals

- Add an opt-in `?sort=<key>` query parameter to all six paginated list endpoints.
- Keep the default behaviour byte-identical for clients that don't send `?sort=`.
- Detect and reject cursor/sort mismatch with a clear 400, never return wrong rows silently.
- Expose the feature through both the `relay` CLI and the MCP tools.

## Non-goals

- Multi-key sort (e.g., `?sort=priority,-created_at`). Single key + `id` tiebreaker only.
- Sort on already-filtered list variants (e.g., `?status=running&sort=name` on jobs).
- Sort by joined columns (e.g., jobs by submitter name) — would need a different cursor scheme.
- Python SDK changes. The Python SDK has a pre-existing pagination gap tracked separately at [bug-2026-05-26-python-sdk-list-pagination-envelope](../../backlog/bug-2026-05-26-python-sdk-list-pagination-envelope.md); sort support lands in the SDK when that bug is fixed.

## Public API Contract

### Query syntax

- `?sort=<key>` — ascending by `<key>`, tiebreak by `id ASC`.
- `?sort=-<key>` — descending by `<key>`, tiebreak by `id DESC`.
- Absent → unchanged default: `created_at DESC, id DESC`.

The leading `-` is the only direction syntax. `?sort=name asc`, `?sort=name:desc`, and `?order=desc` are all rejected with 400.

### Validation

- `<key>` must be in the per-endpoint allowlist (table below). Unknown key → `400 {"error":"unsupported sort key 'X' for /v1/jobs; supported: created_at, name, priority, status, updated_at"}`.
- `?sort=` with empty value → `400 invalid sort`.
- Cursor's encoded sort string must equal the request's `?sort=` (or both resolve to the historical default) → `400 cursor sort key does not match requested sort; drop the cursor or change the sort`.

### Per-endpoint sort-key allowlist

| Endpoint | Default (no `?sort=`) | Additional allowed keys |
|---|---|---|
| `/v1/jobs` | `created_at` desc | `name`, `priority`, `status`, `updated_at` |
| `/v1/workers` | `created_at` desc | `name`, `status`, `last_seen_at` |
| `/v1/users` | `created_at` desc | `name`, `email` |
| `/v1/scheduled-jobs` | `created_at` desc | `name`, `next_run_at`, `updated_at` |
| `/v1/reservations` | `created_at` desc | `name`, `starts_at`, `ends_at` |
| `/v1/agent-enrollments` | `created_at` desc | `expires_at` |

Deliberately excluded for now: JSONB columns (no useful ordering, GIN-only indexing), `submitted_by` UUIDs (meaningless to humans), `hostname` (redundant with `name`).

### Cursor envelope

The opaque cursor's decoded JSON gains a sort field:

```go
type cursorWire struct {
    T string `json:"t"`           // timestamp value (when sort key is a timestamp)
    I string `json:"i"`           // last-seen row id
    S string `json:"s,omitempty"` // verbatim sort string; missing → historical default
    V string `json:"v,omitempty"` // sort column value (when sort key is text)
}
```

Pre-feature cursors (no `s` field) decode under the historical default `"-created_at"`, so old cursors keep working when the client doesn't also switch sort.

### Backward compatibility

- Default response shape is identical when the client sends no `?sort=`.
- Old cursors decode under the historical default.
- New clients sending `?sort=` against an old server: the param is silently ignored and the default order is returned. Documented in the README CLI compatibility note. Acceptable graceful degradation.

## Server-side Internals

### `internal/api/pagination.go` changes

```go
// sortSpec is per-endpoint configuration passed into parsePage.
type sortSpec struct {
    Default string                 // e.g. "-created_at"; used when ?sort= absent
    Keys    map[string]sortKeyKind // allowlist; value type drives cursor population
}

type sortKeyKind int
const (
    sortKeyTimestamp sortKeyKind = iota // populates cursor.T
    sortKeyText                         // populates cursor.StrVal
)

type cursor struct {
    Set    bool
    Sort   string         // canonical sort string the cursor was issued for
    T      time.Time      // populated when sort key is a timestamp
    StrVal string         // populated when sort key is text
    ID     pgtype.UUID
}
```

### Function signatures

```go
// parsePage gains a spec arg; validates ?sort= against the allowlist and
// the cursor's encoded sort against the request's sort.
func parsePage(w http.ResponseWriter, r *http.Request, spec sortSpec) (pageParams, bool)

// buildPage's key callback returns (sortVal any, id) so the cursor can be
// populated with either a timestamp or a string depending on the active sort.
func buildPage[Row, Out any](
    rows []Row,
    limit int32,
    sort string,
    conv func(Row) Out,
    key func(Row) (sortVal any, id pgtype.UUID),
) ([]Out, string)
```

### Per-endpoint integration

Each handler in `internal/api/{jobs,workers,users,scheduled_jobs,reservations,agent_enrollments}.go`:

1. Defines a package-level `var <name>SortSpec = sortSpec{...}`.
2. Passes it to `parsePage(w, r, <name>SortSpec)`.
3. Switches on the canonical sort string to choose the right sqlc query (see SQL layer).
4. Passes the active sort string to `buildPage` so the emitted cursor encodes it.

## SQL Layer

sqlc cannot parameterize `ORDER BY` columns or `WHERE` operators. Generating per-(table, key, direction) sqlc queries is the chosen approach because the alternative (a hand-written query builder) loses sqlc's compile-time typing for the most-used queries in the API.

### Per-endpoint query generation

For each endpoint's primary list query, add `2 × (allowlist size − 1)` companion queries (the existing default sort already covers one slot). Naming pattern: `ListJobsWithEmailPageBy<Key><Dir>`.

| Endpoint | Primary query | New queries |
|---|---|---|
| jobs | `ListJobsWithEmailPage` | 8 (4 keys × 2 dirs) |
| workers | `ListWorkersPage` | 6 |
| users | `ListUsersPage` + `ListUsersIncludingArchivedPage` | 8 (2 keys × 2 × 2) |
| scheduled-jobs | `ListScheduledJobsPage` + `ListScheduledJobsByOwnerPage` | 12 (3 × 2 × 2) |
| reservations | `ListReservationsPage` | 6 |
| agent-enrollments | `ListActiveAgentEnrollmentsPage` | 2 |
| **Total** | | **~42 generated queries** |

All queries share the same template, differing only in the column name and the direction of the comparison/ordering:

```sql
-- ascending example
WHERE (NOT @cursor_set::bool) OR (col, id) > (@cursor_v, @cursor_id)
ORDER BY col ASC, id ASC
LIMIT @page_limit;

-- descending example (existing pattern, generalized)
WHERE (NOT @cursor_set::bool) OR (col, id) < (@cursor_v, @cursor_id)
ORDER BY col DESC, id DESC
LIMIT @page_limit;
```

### Filtered variants

Two kinds of variants exist and they are treated differently:

- **Caller-supplied filter variants** keep the default sort only. `?sort=` combined with `?status=` or `?scheduled_job_id=` on `/v1/jobs` returns `400 sort not supported on filtered list variant; remove the filter or remove the sort`. Loud failure is consistent with the rest of the validation strategy. Generating the full per-key matrix for these is deferred until concrete demand surfaces.
- **Auth-driven variants** support sort on the full allowlist. The user-list archived/active split (`ListUsersPage` vs `ListUsersIncludingArchivedPage`) and the schedules owner/admin split (`ListScheduledJobsByOwnerPage` vs `ListScheduledJobsPage`) happen based on the caller's role, not a query param — the caller cannot opt out of the split, so denying sort on these would be denying sort on the endpoint entirely. Each auth-driven variant gets its own per-key/direction sqlc queries; that is what the doubled counts in the "users (both primaries)" and "scheduled-jobs (both primaries)" rows of the query-count table reflect.

### Nullable-timestamp keys

`last_seen_at` (workers), `starts_at`/`ends_at` (reservations), `expires_at` (agent-enrollments) need explicit NULL handling:

- Index: `(col NULLS LAST, id)` for the desc query, `(col NULLS FIRST, id)` for asc.
- Cursor `WHERE` clause needs a null-vs-value branch — null rows must appear at the correct end of the ordered result.

### Index migration

A single migration `internal/store/migrations/0NNN_paginated_sort_indexes.up.sql` adds all composite indexes:

```sql
-- jobs
CREATE INDEX idx_jobs_name_id     ON jobs (name, id);
CREATE INDEX idx_jobs_priority_id ON jobs (priority, id);
CREATE INDEX idx_jobs_status_id   ON jobs (status, id);
CREATE INDEX idx_jobs_updated_id  ON jobs (updated_at, id);

-- workers
CREATE INDEX idx_workers_name_id        ON workers (name, id);
CREATE INDEX idx_workers_status_id      ON workers (status, id);
CREATE INDEX idx_workers_last_seen_asc  ON workers (last_seen_at NULLS FIRST, id);
CREATE INDEX idx_workers_last_seen_desc ON workers (last_seen_at NULLS LAST, id);

-- users
CREATE INDEX idx_users_name_id  ON users (name, id);
CREATE INDEX idx_users_email_id ON users (email, id);

-- scheduled_jobs
CREATE INDEX idx_scheduled_jobs_name_id        ON scheduled_jobs (name, id);
CREATE INDEX idx_scheduled_jobs_next_run_id    ON scheduled_jobs (next_run_at, id);
CREATE INDEX idx_scheduled_jobs_updated_id     ON scheduled_jobs (updated_at, id);

-- reservations
CREATE INDEX idx_reservations_name_id       ON reservations (name, id);
CREATE INDEX idx_reservations_starts_asc    ON reservations (starts_at NULLS FIRST, id);
CREATE INDEX idx_reservations_starts_desc   ON reservations (starts_at NULLS LAST, id);
CREATE INDEX idx_reservations_ends_asc      ON reservations (ends_at NULLS FIRST, id);
CREATE INDEX idx_reservations_ends_desc     ON reservations (ends_at NULLS LAST, id);

-- agent_enrollments
CREATE INDEX idx_agent_enrollments_expires_asc  ON agent_enrollments (expires_at, id);
```

`(col, id)` works for both `ASC` and `DESC` scans on Postgres; nullable timestamps need both orderings explicitly. Down migration drops each with `IF EXISTS`.

## Client Surfaces

### `relay` CLI (`internal/cli/`)

Each list subcommand gets a `--sort <key>` flag that passes through verbatim:

- `relay jobs list --sort -priority`
- `relay workers list --sort name`
- `relay users list --sort email`
- `relay schedules list --sort next_run_at`
- `relay reservations list --sort -ends_at`

The CLI does not duplicate the server-side allowlist; a stale CLI allowlist is worse than a clean 400 from the server. `FetchAllPages[T]` in `internal/relayclient` already forwards arbitrary query params; no changes there.

### MCP tools (`internal/mcp/`)

Each list-wrapping tool gains an optional `sort` string field on its input struct:

- `relay_list_jobs`
- `relay_list_workers`
- `relay_list_schedules`
- `relay_list_reservations`

The tool description spells out the allowed sort keys inline so the LLM client picks valid values without needing external docs:

```
sort (optional): Sort order. One of "created_at", "-created_at" (default),
"name", "-name", "priority", "-priority", "status", "-status",
"updated_at", "-updated_at". Prefix "-" reverses to descending.
```

This is the one place the allowlist is duplicated — to make the LLM-facing surface self-describing. A unit test asserts each tool's documented options are a subset of the server's allowlist for that endpoint, so drift fails CI.

## Testing Strategy

### Tier 1 — unit tests on the pagination layer

`internal/api/pagination_test.go`:

- Round-trip the new cursor shape for both value-type variants (timestamp key, text key).
- Pre-feature cursor compat: synthesize a cursor with no `s` field, decode under a spec whose default is `-created_at`, assert `cursor.Sort == "-created_at"` and no error.
- `parsePage` validation matrix, table-driven:
  - `?sort=name` with allowed → ok, parsed asc.
  - `?sort=-name` → ok, parsed desc.
  - `?sort=labels` (unknown key) → 400 with allowlist in body.
  - `?sort=` (empty) → 400.
  - `?sort=name asc` (wrong syntax) → 400.
  - Cursor with `s=-created_at`, request with `?sort=name` → 400 mismatch.
  - Cursor with `s=-created_at`, request with no `?sort=` → ok (default matches).
- `buildPage` cursor population for both value-type paths.

### Tier 2 — per-endpoint integration tests

`//go:build integration`. One file per endpoint (`jobs_sort_integration_test.go`, etc.), seeding ~10 rows whose default-sort order differs from each non-default sort. For each key in the allowlist:

- `?sort=<key>` no cursor → returned items in expected order.
- `?sort=-<key>` → reverse order.
- `?limit=3` paginated walk over 10 rows → reassembled sequence matches single-page order.
- Cursor obtained from `?sort=-name` resent with `?sort=name` → 400.

Driven by a table over the allowlist so future sort keys get coverage for free.

### Tier 3 — cross-cutting

- MCP description vs server allowlist drift test.
- Index existence test: query `pg_indexes` after migration, assert each `(col, id)` index exists.
- CLI flag round-trip test: one unit test per list command using `httptest.Server` to assert `--sort -priority` becomes `?sort=-priority` on the wire.

### Manual performance check (not committed)

Before merge, `EXPLAIN ANALYZE` each new sort path against a ~100k-row dev `jobs` table. Confirm the planner picks the composite index and that the cursor `WHERE` becomes an index range scan rather than a sort node. Any seq-scan fallback → fix the index before merging. Document EXPLAIN output in the retro.

## Documentation

- `README.md` "List endpoints" subsection — extend with `?sort=` semantics, the dash convention, and the per-endpoint allowlist table.
- `README.md` MCP section — note the new optional `sort` parameter on the four affected tools.
- `relay <cmd> list --help` — `--sort` description (free from the flag definition).

## Rollout

Single PR, no feature flag. The change is purely additive:

- New query param is optional; default response shape is unchanged.
- Old cursors decode under the historical default.
- Indexes are non-blocking to create on Postgres for tables this size.

No client/server version coordination required. Old clients keep working unchanged; new clients sending `--sort` against an old server silently fall back to the default order (acceptable graceful degradation).

## File Inventory

Concrete list of files touched, for the implementation plan:

- `internal/api/pagination.go` — extend cursor, `parsePage`, `buildPage`; add `sortSpec`.
- `internal/api/pagination_test.go` — Tier 1 tests.
- `internal/api/jobs.go`, `workers.go`, `users.go`, `scheduled_jobs.go`, `reservations.go`, `agent_enrollments.go` — per-endpoint `sortSpec` + dispatch switch.
- `internal/api/{jobs,workers,users,scheduled_jobs,reservations,agent_enrollments}_sort_integration_test.go` — Tier 2 tests, six new files.
- `internal/store/query/{jobs,workers,users,scheduled_jobs,reservations,agent_enrollments}.sql` — per-key/direction queries (~42 new).
- `internal/store/migrations/0NNN_paginated_sort_indexes.up.sql` / `.down.sql` — composite indexes.
- `internal/cli/jobs.go`, `workers.go`, `users.go`, `schedules.go`, `reservations.go` — `--sort` flag.
- `internal/mcp/jobs.go`, `workers.go`, `schedules.go`, `reservations.go` — `sort` input field + description.
- `README.md` — REST + MCP doc sections.
