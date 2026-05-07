# List Endpoint Pagination Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add cursor-based pagination across all six unbounded list endpoints (`/v1/jobs`, `/v1/workers`, `/v1/users`, `/v1/scheduled-jobs`, `/v1/agent-enrollments`, `/v1/reservations`) with a wrapped JSON envelope contract.

**Architecture:** A single `internal/api/pagination.go` module provides the cursor codec (`base64url(JSON{"t","i"})`), request parser (`parsePage`), response envelope (`page[T]`), and page builder (`buildPage`). Six handlers and six CLI commands consume those helpers. SQL queries get paginated `*Page` + `Count*` variants; legacy queries are kept only where caller audits show non-API consumers, otherwise deleted. One migration adds composite `(created_at DESC, id DESC)` indexes per resource and drops single-column indexes that the new composites supersede.

**Tech Stack:** Go 1.22+ (generics), sqlc, pgx/v5 (`pgtype.UUID`, `pgtype.Timestamptz`), golang-migrate (embedded), testify, testcontainers-go.

**Spec:** [docs/superpowers/specs/2026-05-06-list-endpoint-pagination-design.md](../specs/2026-05-06-list-endpoint-pagination-design.md)

**Caller audit (already completed during planning):**

| Legacy query | Non-API callers | Verdict |
|---|---|---|
| `ListWorkers` | `internal/scheduler/dispatch.go:74` | **Keep** |
| `ListJobsByScheduledJob` | `internal/schedrunner/runner_test.go:97,139` | **Keep** |
| `ListActiveAgentEnrollments` | `internal/store/agent_enrollments_test.go:94` | **Keep** |
| `ListActiveReservations` | `internal/scheduler/dispatch.go:79` | **Keep** |
| `ListJobs` | none (orphan) | **Delete** |
| `ListJobsByStatus` | none (orphan) | **Delete** |
| `ListJobsWithEmail` | only `internal/api/jobs.go` | **Delete** after handler migration |
| `ListJobsByStatusWithEmail` | only `internal/api/jobs.go` | **Delete** after handler migration |
| `ListUsers` | only `internal/api/users.go` | **Delete** after handler migration |
| `ListUsersIncludingArchived` | only `internal/api/users.go` | **Delete** after handler migration |
| `ListScheduledJobs` | only `internal/api/scheduled_jobs.go` | **Delete** after handler migration |
| `ListScheduledJobsByOwner` | only `internal/api/scheduled_jobs.go` | **Delete** after handler migration |
| `ListReservations` | only `internal/api/reservations.go` | **Delete** after handler migration |

**Index audit (already completed during planning):** `idx_jobs_status` (000001), `idx_jobs_scheduled_job_id` (000006), `idx_scheduled_jobs_owner` (000006). All three are subsumed by the composites in 000011 and will be dropped.

---

## Phase 1 — Foundation (sequential)

These three tasks build the shared pagination helpers. Phase 2 onward depends on them.

---

### Task 1: Cursor codec — `encodeCursor` / `decodeCursor`

**Files:**
- Create: `internal/api/pagination.go`
- Create: `internal/api/pagination_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/api/pagination_test.go`:

```go
package api

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCursor_RoundTrip(t *testing.T) {
	id := pgtype.UUID{Valid: true}
	copy(id.Bytes[:], []byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0xfe, 0xdc, 0xba, 0x98, 0x76, 0x54, 0x32, 0x10})
	tt := time.Date(2026, 4, 16, 10, 30, 45, 123456000, time.UTC) // µs precision

	enc := encodeCursor(tt, id)
	require.NotEmpty(t, enc)

	got, err := decodeCursor(enc)
	require.NoError(t, err)
	require.True(t, got.Set)
	assert.True(t, got.T.Equal(tt), "decoded time %v != original %v", got.T, tt)
	assert.Equal(t, id, got.ID)
}

func TestCursor_TruncatesNanos(t *testing.T) {
	// Postgres timestamptz is microsecond precision. The cursor codec must
	// truncate nanos on encode so a strict (created_at, id) < (cursor_ts, ...)
	// comparison won't accidentally skip the row at the boundary.
	id := pgtype.UUID{Valid: true}
	tt := time.Date(2026, 4, 16, 10, 30, 45, 123456789, time.UTC)
	expected := tt.Truncate(time.Microsecond)

	enc := encodeCursor(tt, id)
	got, err := decodeCursor(enc)
	require.NoError(t, err)
	assert.True(t, got.T.Equal(expected), "got %v, want %v", got.T, expected)
}

func TestCursor_Empty(t *testing.T) {
	got, err := decodeCursor("")
	require.NoError(t, err)
	assert.False(t, got.Set, "empty cursor must yield Set=false")
}

func TestCursor_InvalidBase64(t *testing.T) {
	_, err := decodeCursor("not!valid!base64!")
	assert.ErrorIs(t, err, errBadCursor)
}

func TestCursor_InvalidJSON(t *testing.T) {
	// Valid base64 wrapping non-JSON contents.
	_, err := decodeCursor("bm90LWpzb24") // base64url("not-json")
	assert.ErrorIs(t, err, errBadCursor)
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/api/ -run TestCursor -v
```

Expected: compile errors — `encodeCursor`, `decodeCursor`, `errBadCursor` undefined.

- [ ] **Step 3: Implement the cursor codec**

Create `internal/api/pagination.go`:

```go
package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

var errBadCursor = errors.New("invalid cursor")

// cursor is the decoded form of an opaque pagination cursor.
type cursor struct {
	Set bool        // false → first page (no cursor sent)
	T   time.Time   // last-seen created_at, microsecond precision
	ID  pgtype.UUID // last-seen row id (tiebreaker)
}

// cursorWire is the JSON shape encoded inside the base64 envelope.
type cursorWire struct {
	T string `json:"t"`
	I string `json:"i"`
}

// encodeCursor serializes (t, id) as base64url(JSON). The timestamp is
// truncated to microsecond precision: Postgres timestamptz is µs-precise,
// and a nanosecond-precise cursor would skip the boundary row when the
// query does (created_at, id) < (cursor_ts, cursor_id).
func encodeCursor(t time.Time, id pgtype.UUID) string {
	tUTC := t.UTC().Truncate(time.Microsecond)
	w := cursorWire{
		T: tUTC.Format(time.RFC3339Nano),
		I: uuidStr(id),
	}
	b, _ := json.Marshal(w)
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeCursor reverses encodeCursor. Empty input yields a zero cursor with
// Set=false (used for first-page requests). Malformed input returns
// errBadCursor; the caller MUST translate this to a 400 response and MUST
// NOT echo decoded bytes to the client.
func decodeCursor(s string) (cursor, error) {
	if s == "" {
		return cursor{}, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursor{}, errBadCursor
	}
	var w cursorWire
	if err := json.Unmarshal(b, &w); err != nil {
		return cursor{}, errBadCursor
	}
	t, err := time.Parse(time.RFC3339Nano, w.T)
	if err != nil {
		return cursor{}, errBadCursor
	}
	id, err := parseUUID(w.I)
	if err != nil {
		return cursor{}, errBadCursor
	}
	return cursor{Set: true, T: t, ID: id}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/api/ -run TestCursor -v
```

Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/api/pagination.go internal/api/pagination_test.go
git commit -m "feat(api): add cursor codec for pagination"
```

---

### Task 2: Page params parser — `parsePage`

**Files:**
- Modify: `internal/api/pagination.go`
- Modify: `internal/api/pagination_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/api/pagination_test.go`:

```go
import (
	"net/http/httptest"
	// ... keep existing imports
)

func TestParsePage_Defaults(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/jobs", nil)
	w := httptest.NewRecorder()
	pp, ok := parsePage(w, r)
	require.True(t, ok)
	assert.Equal(t, defaultLimit, pp.Limit)
	assert.False(t, pp.Cursor.Set)
}

func TestParsePage_LimitClamping(t *testing.T) {
	cases := []struct {
		name    string
		query   string
		wantOK  bool
		wantLim int32
	}{
		{"valid mid", "?limit=37", true, 37},
		{"max", "?limit=200", true, 200},
		{"zero rejected", "?limit=0", false, 0},
		{"negative rejected", "?limit=-5", false, 0},
		{"over max rejected", "?limit=201", false, 0},
		{"non-numeric rejected", "?limit=abc", false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/v1/jobs"+tc.query, nil)
			w := httptest.NewRecorder()
			pp, ok := parsePage(w, r)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.wantLim, pp.Limit)
			}
		})
	}
}

func TestParsePage_BadCursor(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/jobs?cursor=garbage!!!", nil)
	w := httptest.NewRecorder()
	_, ok := parsePage(w, r)
	assert.False(t, ok)
	assert.Equal(t, 400, w.Code)
}
```

Adjust the import block at the top of the file to add `"net/http/httptest"`.

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/api/ -run TestParsePage -v
```

Expected: compile errors — `parsePage`, `pageParams`, `defaultLimit` undefined.

- [ ] **Step 3: Implement parsePage**

Append to `internal/api/pagination.go`:

```go
import (
	// add to existing imports:
	"net/http"
	"strconv"
)

const (
	defaultLimit int32 = 50
	maxLimit     int32 = 200
)

// pageParams captures validated pagination input from the URL query string.
type pageParams struct {
	Limit  int32
	Cursor cursor
}

// CursorTs returns the cursor timestamp as a pgtype.Timestamptz. The Valid
// flag tracks whether the cursor was actually sent (first-page requests
// produce a zero, invalid value).
func (p pageParams) CursorTs() pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: p.Cursor.T, Valid: p.Cursor.Set}
}

// LimitPlusOne returns Limit+1 — handlers pass this to the SQL layer so
// queries fetch one extra row, used by buildPage to detect "more available"
// without a follow-up COUNT.
func (p pageParams) LimitPlusOne() int32 {
	return p.Limit + 1
}

// parsePage extracts ?limit= and ?cursor= from the request. On invalid
// input it writes the 400 response itself and returns ok=false. Defaults:
// limit=50. Range: [1, 200]. Bad cursor → 400 with body "invalid cursor".
func parsePage(w http.ResponseWriter, r *http.Request) (pageParams, bool) {
	pp := pageParams{Limit: defaultLimit}

	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil || n < 1 || n > int64(maxLimit) {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return pageParams{}, false
		}
		pp.Limit = int32(n)
	}

	c, err := decodeCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid cursor")
		return pageParams{}, false
	}
	pp.Cursor = c
	return pp, true
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/api/ -run TestParsePage -v
```

Expected: PASS (3 test functions, 8 sub-cases).

- [ ] **Step 5: Commit**

```bash
git add internal/api/pagination.go internal/api/pagination_test.go
git commit -m "feat(api): add pagination request parser"
```

---

### Task 3: Page envelope and `buildPage` helper

**Files:**
- Modify: `internal/api/pagination.go`
- Modify: `internal/api/pagination_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/api/pagination_test.go`:

```go
func TestBuildPage_NoMore(t *testing.T) {
	type row struct {
		t  time.Time
		id pgtype.UUID
	}
	id := pgtype.UUID{Valid: true}
	rows := []row{
		{time.Now(), id},
		{time.Now(), id},
	}
	conv := func(r row) string { return "x" }
	key := func(r row) (time.Time, pgtype.UUID) { return r.t, r.id }
	items, next := buildPage(rows, 50, conv, key)
	assert.Len(t, items, 2)
	assert.Empty(t, next, "next_cursor must be empty when fewer rows than limit")
}

func TestBuildPage_HasMore(t *testing.T) {
	type row struct {
		t  time.Time
		id pgtype.UUID
	}
	id := pgtype.UUID{Valid: true}
	t0 := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)
	rows := []row{
		{t0.Add(3 * time.Second), id},
		{t0.Add(2 * time.Second), id},
		{t0.Add(1 * time.Second), id},
	}
	conv := func(r row) string { return "x" }
	key := func(r row) (time.Time, pgtype.UUID) { return r.t, r.id }
	items, next := buildPage(rows, 2, conv, key) // limit=2, got 3 ⇒ trim, emit cursor
	assert.Len(t, items, 2)
	require.NotEmpty(t, next, "must emit cursor when limit+1 rows fetched")

	// Cursor must encode the LAST kept row's (t, id), not the trimmed extra.
	c, err := decodeCursor(next)
	require.NoError(t, err)
	assert.True(t, c.T.Equal(rows[1].t.Truncate(time.Microsecond)))
}

func TestBuildPage_EmptyResult(t *testing.T) {
	type row struct {
		t  time.Time
		id pgtype.UUID
	}
	conv := func(r row) string { return "x" }
	key := func(r row) (time.Time, pgtype.UUID) { return r.t, r.id }
	items, next := buildPage([]row{}, 50, conv, key)
	assert.Empty(t, items)
	assert.Empty(t, next, "empty result must yield empty cursor, not echo input")
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/api/ -run TestBuildPage -v
```

Expected: compile errors — `buildPage` undefined.

- [ ] **Step 3: Implement page envelope and buildPage**

Append to `internal/api/pagination.go`:

```go
// page is the JSON envelope for paginated list endpoints.
type page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor"`
	Total      int64  `json:"total"`
}

// buildPage trims a (limit+1)-row fetch result to limit rows and emits the
// cursor pointing at the LAST KEPT row's key — never the trimmed extra row.
//
// - Fewer than limit+1 rows fetched → no cursor (last page).
// - Empty input → empty items, empty cursor (do not echo input cursor).
// - Otherwise → trim to limit, encode cursor from items[limit-1].
func buildPage[Row, Out any](
	rows []Row,
	limit int32,
	conv func(Row) Out,
	key func(Row) (time.Time, pgtype.UUID),
) ([]Out, string) {
	if len(rows) == 0 {
		return []Out{}, ""
	}
	hasMore := int32(len(rows)) > limit
	if hasMore {
		rows = rows[:limit]
	}
	items := make([]Out, len(rows))
	for i, r := range rows {
		items[i] = conv(r)
	}
	if !hasMore {
		return items, ""
	}
	last := rows[len(rows)-1]
	t, id := key(last)
	return items, encodeCursor(t, id)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/api/ -run TestBuildPage -v
```

Expected: PASS (3 tests).

- [ ] **Step 5: Run the full pagination test file**

```bash
go test ./internal/api/ -run TestCursor -v
go test ./internal/api/ -run TestParsePage -v
go test ./internal/api/ -run TestBuildPage -v
```

Expected: all green (11 tests total).

- [ ] **Step 6: Commit**

```bash
git add internal/api/pagination.go internal/api/pagination_test.go
git commit -m "feat(api): add page envelope and buildPage helper"
```

---

## Phase 2 — Database (sequential)

Phase 2 changes block all subsequent handler work because handlers depend on sqlc-generated types. Do not start Phase 3 until Phase 2 is complete.

---

### Task 4: Migration 000011 — pagination indexes

**Files:**
- Create: `internal/store/migrations/000011_pagination_indexes.up.sql`
- Create: `internal/store/migrations/000011_pagination_indexes.down.sql`

This task adds the migration file. We verify it applies and rolls back via `make test-integration` after Task 5 (which adds queries that exercise the indexes).

- [ ] **Step 1: Create the up migration**

`internal/store/migrations/000011_pagination_indexes.up.sql`:

```sql
-- Composite indexes supporting cursor pagination over (created_at, id).
-- All paginated list queries ORDER BY created_at DESC, id DESC and apply
-- (created_at, id) < (cursor_ts, cursor_id) when a cursor is present.
-- These indexes let Postgres serve those queries via Index Scan.

CREATE INDEX idx_jobs_created_id          ON jobs(created_at DESC, id DESC);
CREATE INDEX idx_jobs_status_created_id   ON jobs(status, created_at DESC, id DESC);
CREATE INDEX idx_jobs_sched_created_id    ON jobs(scheduled_job_id, created_at DESC, id DESC) WHERE scheduled_job_id IS NOT NULL;
CREATE INDEX idx_workers_created_id       ON workers(created_at DESC, id DESC);
CREATE INDEX idx_users_created_id         ON users(created_at DESC, id DESC);
CREATE INDEX idx_sched_jobs_created_id    ON scheduled_jobs(created_at DESC, id DESC);
CREATE INDEX idx_sched_jobs_owner_created ON scheduled_jobs(owner_id, created_at DESC, id DESC);
CREATE INDEX idx_agent_enr_created_id     ON agent_enrollments(created_at DESC, id DESC) WHERE consumed_at IS NULL;
CREATE INDEX idx_reservations_created_id  ON reservations(created_at DESC, id DESC);

-- Single-column indexes superseded by the composites above.
DROP INDEX IF EXISTS idx_jobs_status;            -- 000001_initial
DROP INDEX IF EXISTS idx_jobs_scheduled_job_id;  -- 000006_scheduled_jobs
DROP INDEX IF EXISTS idx_scheduled_jobs_owner;   -- 000006_scheduled_jobs
```

- [ ] **Step 2: Create the down migration**

`internal/store/migrations/000011_pagination_indexes.down.sql`:

```sql
-- Recreate single-column indexes that 000011 dropped, then drop the composites.

CREATE INDEX idx_jobs_status            ON jobs(status);
CREATE INDEX idx_jobs_scheduled_job_id  ON jobs(scheduled_job_id);
CREATE INDEX idx_scheduled_jobs_owner   ON scheduled_jobs(owner_id);

DROP INDEX IF EXISTS idx_jobs_created_id;
DROP INDEX IF EXISTS idx_jobs_status_created_id;
DROP INDEX IF EXISTS idx_jobs_sched_created_id;
DROP INDEX IF EXISTS idx_workers_created_id;
DROP INDEX IF EXISTS idx_users_created_id;
DROP INDEX IF EXISTS idx_sched_jobs_created_id;
DROP INDEX IF EXISTS idx_sched_jobs_owner_created;
DROP INDEX IF EXISTS idx_agent_enr_created_id;
DROP INDEX IF EXISTS idx_reservations_created_id;
```

- [ ] **Step 3: Verify the build still passes**

```bash
make build
```

Expected: all three binaries build successfully (the migration files are embedded but not yet exercised).

- [ ] **Step 4: Commit**

```bash
git add internal/store/migrations/000011_pagination_indexes.up.sql internal/store/migrations/000011_pagination_indexes.down.sql
git commit -m "feat(db): add pagination composite indexes (migration 000011)"
```

---

### Task 5: Add paginated SQL queries to all six query files + run `make generate`

**Files:**
- Modify: `internal/store/query/jobs.sql`
- Modify: `internal/store/query/workers.sql`
- Modify: `internal/store/query/users.sql`
- Modify: `internal/store/query/scheduled_jobs.sql`
- Modify: `internal/store/query/agent_enrollments.sql`
- Modify: `internal/store/query/reservations.sql`
- Auto-generated by `make generate`: `internal/store/*.sql.go`, `internal/store/models.go`

Every paginated query uses the same shape:

```sql
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (<table>.created_at, <table>.id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY <table>.created_at DESC, <table>.id DESC
LIMIT sqlc.arg(page_limit)::int + 1
```

`<table>` is qualified to disambiguate when joining `users` for email. The `+ 1` lives in SQL, not the handler — handlers pass the user-requested limit and the query fetches one extra row.

- [ ] **Step 1: Update `internal/store/query/jobs.sql`**

Replace the file's contents with:

```sql
-- name: CreateJob :one
INSERT INTO jobs (name, priority, submitted_by, labels, scheduled_job_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetJob :one
SELECT * FROM jobs WHERE id = $1;

-- name: GetJobWithEmail :one
SELECT j.*, u.email AS submitted_by_email
FROM jobs j
JOIN users u ON u.id = j.submitted_by
WHERE j.id = $1;

-- name: ListJobsWithEmailPage :many
SELECT j.*, u.email AS submitted_by_email
FROM jobs j
JOIN users u ON u.id = j.submitted_by
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (j.created_at, j.id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY j.created_at DESC, j.id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountJobs :one
SELECT COUNT(*) FROM jobs;

-- name: ListJobsByStatusWithEmailPage :many
SELECT j.*, u.email AS submitted_by_email
FROM jobs j
JOIN users u ON u.id = j.submitted_by
WHERE j.status = sqlc.arg(status)::text
  AND (sqlc.arg(cursor_set)::bool = FALSE
       OR (j.created_at, j.id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY j.created_at DESC, j.id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountJobsByStatus :one
SELECT COUNT(*) FROM jobs WHERE status = $1;

-- name: ListJobsByScheduledJobWithEmailPage :many
SELECT j.*, u.email AS submitted_by_email
FROM jobs j
JOIN users u ON u.id = j.submitted_by
WHERE j.scheduled_job_id = sqlc.arg(scheduled_job_id)::uuid
  AND (sqlc.arg(cursor_set)::bool = FALSE
       OR (j.created_at, j.id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY j.created_at DESC, j.id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountJobsByScheduledJob :one
SELECT COUNT(*) FROM jobs WHERE scheduled_job_id = $1;

-- name: UpdateJobStatus :one
UPDATE jobs
SET status = $2, updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: DeleteJob :exec
DELETE FROM jobs WHERE id = $1;

-- name: ListJobsByScheduledJob :many
-- Internal use only (schedrunner tests). Not paginated.
SELECT j.*, u.email AS submitted_by_email
FROM jobs j
JOIN users u ON u.id = j.submitted_by
WHERE j.scheduled_job_id = $1
ORDER BY j.created_at DESC;
```

Note: `ListJobs`, `ListJobsByStatus`, `ListJobsWithEmail`, `ListJobsByStatusWithEmail` are deleted. `ListJobsByScheduledJob` (no email — wait, the original DOES have email; check `internal/store/query/jobs.sql:43-48`). The original `ListJobsByScheduledJob` already joined for email. We keep that variant as-is for schedrunner tests; the new paginated variant has the suffix `WithEmailPage` to distinguish.

Wait — the original `ListJobsByScheduledJob` joined for email. After this change there are two queries: `ListJobsByScheduledJob` (kept, unbounded, used by tests) and `ListJobsByScheduledJobWithEmailPage` (new, used by API). They differ only in pagination. That's intentional: tests want a deterministic full-list dump without paging.

- [ ] **Step 2: Update `internal/store/query/workers.sql`**

Add at the end (do not modify existing queries):

```sql
-- name: ListWorkersPage :many
SELECT * FROM workers
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (created_at, id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountWorkers :one
SELECT COUNT(*) FROM workers;
```

`ListWorkers` (sorts by `name`) is preserved — used by `internal/scheduler/dispatch.go:74`.

- [ ] **Step 3: Update `internal/store/query/users.sql`**

Replace the `ListUsers` and `ListUsersIncludingArchived` queries with their paginated variants. Final content of those two query blocks:

```sql
-- name: ListUsersPage :many
SELECT id, email, name, is_admin, created_at
FROM users
WHERE archived_at IS NULL
  AND (sqlc.arg(cursor_set)::bool = FALSE
       OR (created_at, id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountUsers :one
SELECT COUNT(*) FROM users WHERE archived_at IS NULL;

-- name: ListUsersIncludingArchivedPage :many
SELECT id, email, name, is_admin, created_at, archived_at
FROM users
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (created_at, id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountUsersIncludingArchived :one
SELECT COUNT(*) FROM users;
```

Delete the original `ListUsers` and `ListUsersIncludingArchived`.

Note: the legacy queries sorted ASC; the paginated variants sort DESC for contract uniformity (see spec §"Behavior changes"). This is documented in the README in Task 22.

- [ ] **Step 4: Update `internal/store/query/scheduled_jobs.sql`**

Replace `ListScheduledJobs` and `ListScheduledJobsByOwner` with:

```sql
-- name: ListScheduledJobsPage :many
SELECT * FROM scheduled_jobs
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (created_at, id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountScheduledJobs :one
SELECT COUNT(*) FROM scheduled_jobs;

-- name: ListScheduledJobsByOwnerPage :many
SELECT * FROM scheduled_jobs
WHERE owner_id = sqlc.arg(owner_id)::uuid
  AND (sqlc.arg(cursor_set)::bool = FALSE
       OR (created_at, id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountScheduledJobsByOwner :one
SELECT COUNT(*) FROM scheduled_jobs WHERE owner_id = $1;
```

Other queries in the file (`CreateScheduledJob`, `GetScheduledJob`, `UpdateScheduledJob`, `DeleteScheduledJob`, `ListEligibleScheduledJobs`, `ListOverdueScheduledJobsForCatchup`, `AdvanceScheduledJob`, `CountActiveJobsForSchedule`) are unchanged.

- [ ] **Step 5: Update `internal/store/query/agent_enrollments.sql`**

Add at the end (do not modify existing queries):

```sql
-- name: ListActiveAgentEnrollmentsPage :many
SELECT id, hostname_hint, created_by, created_at, expires_at
FROM agent_enrollments
WHERE consumed_at IS NULL
  AND expires_at > NOW()
  AND (sqlc.arg(cursor_set)::bool = FALSE
       OR (created_at, id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountActiveAgentEnrollments :one
SELECT COUNT(*) FROM agent_enrollments
WHERE consumed_at IS NULL AND expires_at > NOW();
```

`ListActiveAgentEnrollments` (the legacy non-paginated form) is preserved — used by `internal/store/agent_enrollments_test.go:94`.

- [ ] **Step 6: Update `internal/store/query/reservations.sql`**

Replace `ListReservations` with:

```sql
-- name: ListReservationsPage :many
SELECT * FROM reservations
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (created_at, id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountReservations :one
SELECT COUNT(*) FROM reservations;
```

`ListActiveReservations` (kept, used by scheduler) is unchanged.

- [ ] **Step 7: Run `make generate`**

```bash
make generate
```

Expected: sqlc regenerates `internal/store/*.sql.go` and `internal/store/models.go`. No errors. New methods exist on `*Queries`:
- `ListJobsWithEmailPage`, `CountJobs`, `ListJobsByStatusWithEmailPage`, `CountJobsByStatus`, `ListJobsByScheduledJobWithEmailPage`, `CountJobsByScheduledJob`
- `ListWorkersPage`, `CountWorkers`
- `ListUsersPage`, `CountUsers`, `ListUsersIncludingArchivedPage`, `CountUsersIncludingArchived`
- `ListScheduledJobsPage`, `CountScheduledJobs`, `ListScheduledJobsByOwnerPage`, `CountScheduledJobsByOwner`
- `ListActiveAgentEnrollmentsPage`, `CountActiveAgentEnrollments`
- `ListReservationsPage`, `CountReservations`

Removed methods: `ListJobs`, `ListJobsByStatus`, `ListJobsWithEmail`, `ListJobsByStatusWithEmail`, `ListUsers`, `ListUsersIncludingArchived`, `ListScheduledJobs`, `ListScheduledJobsByOwner`, `ListReservations`.

Kept methods: `ListWorkers`, `ListJobsByScheduledJob`, `ListActiveAgentEnrollments`, `ListActiveReservations`.

- [ ] **Step 8: Verify the build now fails (handlers reference deleted methods)**

```bash
make build 2>&1 | head -40
```

Expected: compile errors in `internal/api/jobs.go`, `users.go`, `scheduled_jobs.go`, `reservations.go`, `workers.go`, `agent_enrollments.go` referencing removed methods. **This is intentional** — handlers haven't been migrated yet. Phase 3 fixes them one at a time.

- [ ] **Step 9: Commit**

```bash
git add internal/store/query/ internal/store/
git commit -m "feat(store): add paginated list queries

Adds *Page + Count* sqlc queries for jobs, workers, users, scheduled_jobs,
agent_enrollments, and reservations. Each paginated query keys on
(created_at DESC, id DESC) with the cursor predicate gated by cursor_set.

Removes legacy non-paginated list queries that had no remaining callers
after the API migration. Keeps ListWorkers (scheduler), ListJobsByScheduledJob
(schedrunner tests), ListActiveAgentEnrollments (store test), and
ListActiveReservations (scheduler) — all preserved for non-API consumers."
```

---

## Phase 3 — Handler migration (parallel-friendly)

Each task in this phase is independent of the others (different handler files, different packages of resource types). All depend on Phase 2 being complete. After each task, the build still has compile errors elsewhere — that is expected; we're chipping away at the broken-build state from Task 5 Step 8 one resource at a time.

When run as subagents, dispatch up to 3 of these in parallel; merging is automatic since they touch different files.

---

### Task 6: `handleListJobs` envelope migration

**Files:**
- Modify: `internal/api/jobs.go` (replace `handleListJobs`, add helper functions)

- [ ] **Step 1: Read current handler**

```bash
sed -n '174,250p' internal/api/jobs.go
```

(Or open the file at line 174 and review through line ~245.)

- [ ] **Step 2: Replace `handleListJobs`**

Open `internal/api/jobs.go` and replace the entire `handleListJobs` function (currently spans roughly lines 174–~245) with:

```go
// jobsRowKey extracts the (created_at, id) sort key from a row returned by
// any of the ListJobs*WithEmailPage queries. All three row types share the
// same field shape, so we duplicate the function rather than introduce an
// interface for two methods.
func jobsRowKey_default(r store.ListJobsWithEmailPageRow) (time.Time, pgtype.UUID) {
	return r.CreatedAt.Time, r.ID
}
func jobsRowKey_byStatus(r store.ListJobsByStatusWithEmailPageRow) (time.Time, pgtype.UUID) {
	return r.CreatedAt.Time, r.ID
}
func jobsRowKey_byScheduled(r store.ListJobsByScheduledJobWithEmailPageRow) (time.Time, pgtype.UUID) {
	return r.CreatedAt.Time, r.ID
}

// jobRowToResponse converts a paginated job row (with email) to the public
// jobResponse. The store row types differ in field set but share name,
// status, etc.; we use small per-type adapters rather than reflect.
func jobRowToResponse_default(r store.ListJobsWithEmailPageRow) jobResponse {
	job := store.Job{
		ID: r.ID, Name: r.Name, Priority: r.Priority, Status: r.Status,
		SubmittedBy: r.SubmittedBy, Labels: r.Labels,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	return toJobResponse(job, r.SubmittedByEmail, nil, nil)
}
func jobRowToResponse_byStatus(r store.ListJobsByStatusWithEmailPageRow) jobResponse {
	job := store.Job{
		ID: r.ID, Name: r.Name, Priority: r.Priority, Status: r.Status,
		SubmittedBy: r.SubmittedBy, Labels: r.Labels,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	return toJobResponse(job, r.SubmittedByEmail, nil, nil)
}
func jobRowToResponse_byScheduled(r store.ListJobsByScheduledJobWithEmailPageRow) jobResponse {
	job := store.Job{
		ID: r.ID, Name: r.Name, Priority: r.Priority, Status: r.Status,
		SubmittedBy: r.SubmittedBy, Labels: r.Labels,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	return toJobResponse(job, r.SubmittedByEmail, nil, nil)
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pp, ok := parsePage(w, r)
	if !ok {
		return
	}

	// Branch 1: ?scheduled_job_id=<uuid>
	if schedIDStr := r.URL.Query().Get("scheduled_job_id"); schedIDStr != "" {
		schedID, err := parseUUID(schedIDStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid scheduled_job_id")
			return
		}
		// Auth gate runs BEFORE pagination — non-owners get 404, not a paginated empty result.
		if _, ok := s.ownedScheduledJob(w, r, schedID); !ok {
			return
		}
		rows, err := s.q.ListJobsByScheduledJobWithEmailPage(ctx, store.ListJobsByScheduledJobWithEmailPageParams{
			ScheduledJobID: schedID,
			CursorSet:      pp.Cursor.Set,
			CursorTs:       pp.CursorTs(),
			CursorID:       pp.Cursor.ID,
			PageLimit:      pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list jobs failed")
			return
		}
		total, err := s.q.CountJobsByScheduledJob(ctx, schedID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "count jobs failed")
			return
		}
		items, next := buildPage(rows, pp.Limit, jobRowToResponse_byScheduled, jobsRowKey_byScheduled)
		writeJSON(w, http.StatusOK, page[jobResponse]{Items: items, NextCursor: next, Total: total})
		return
	}

	// Branch 2: ?status=<status>
	if status := r.URL.Query().Get("status"); status != "" {
		rows, err := s.q.ListJobsByStatusWithEmailPage(ctx, store.ListJobsByStatusWithEmailPageParams{
			Status:    status,
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list jobs failed")
			return
		}
		total, err := s.q.CountJobsByStatus(ctx, status)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "count jobs failed")
			return
		}
		items, next := buildPage(rows, pp.Limit, jobRowToResponse_byStatus, jobsRowKey_byStatus)
		writeJSON(w, http.StatusOK, page[jobResponse]{Items: items, NextCursor: next, Total: total})
		return
	}

	// Default branch: no filter
	rows, err := s.q.ListJobsWithEmailPage(ctx, store.ListJobsWithEmailPageParams{
		CursorSet: pp.Cursor.Set,
		CursorTs:  pp.CursorTs(),
		CursorID:  pp.Cursor.ID,
		PageLimit: pp.Limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list jobs failed")
		return
	}
	total, err := s.q.CountJobs(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "count jobs failed")
		return
	}
	items, next := buildPage(rows, pp.Limit, jobRowToResponse_default, jobsRowKey_default)
	writeJSON(w, http.StatusOK, page[jobResponse]{Items: items, NextCursor: next, Total: total})
}
```

The exact field name on the params struct (`CursorID` vs `CursorId`) depends on sqlc's field-naming for `cursor_id`. After `make generate` in Task 5, sqlc names it `CursorID` (Go convention). If your generated code uses `CursorId` instead, adapt accordingly — sqlc's casing has been consistent in this repo (see existing `WorkerID`, `JobID` fields).

- [ ] **Step 3: Verify the jobs handler compiles**

```bash
go build ./internal/api/
```

Expected: `internal/api/jobs.go` compiles. Other API files may still fail. That's fine.

- [ ] **Step 4: Commit**

```bash
git add internal/api/jobs.go
git commit -m "feat(api): paginate /v1/jobs with envelope response"
```

---

### Task 7: `handleListWorkers` envelope migration

**Files:**
- Modify: `internal/api/workers.go`

- [ ] **Step 1: Replace `handleListWorkers`**

In `internal/api/workers.go`, replace the existing `handleListWorkers` (lines ~51–64) with:

```go
import (
	// add to existing imports:
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func workersRowKey(w store.Worker) (time.Time, pgtype.UUID) {
	return w.CreatedAt.Time, w.ID
}

func (s *Server) handleListWorkers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pp, ok := parsePage(w, r)
	if !ok {
		return
	}

	rows, err := s.q.ListWorkersPage(ctx, store.ListWorkersPageParams{
		CursorSet: pp.Cursor.Set,
		CursorTs:  pp.CursorTs(),
		CursorID:  pp.Cursor.ID,
		PageLimit: pp.Limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list workers failed")
		return
	}
	total, err := s.q.CountWorkers(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "count workers failed")
		return
	}
	items, next := buildPage(rows, pp.Limit, toWorkerResponse, workersRowKey)
	writeJSON(w, http.StatusOK, page[workerResponse]{Items: items, NextCursor: next, Total: total})
}
```

If `time` and `pgtype` are not already imported, add them; if already imported, leave the import block as-is.

- [ ] **Step 2: Verify the workers handler compiles**

```bash
go build ./internal/api/
```

Expected: `internal/api/workers.go` compiles (other files may still fail).

- [ ] **Step 3: Commit**

```bash
git add internal/api/workers.go
git commit -m "feat(api): paginate /v1/workers with envelope response"
```

---

### Task 8: `handleListUsers` envelope migration

**Files:**
- Modify: `internal/api/users.go`

- [ ] **Step 1: Replace `handleListUsers`**

In `internal/api/users.go`, replace the existing `handleListUsers` (lines ~69–116) with:

```go
import (
	// add to existing imports:
	"time"
)

func usersListRowKey(r store.ListUsersPageRow) (time.Time, pgtype.UUID) {
	return r.CreatedAt.Time, r.ID
}
func usersListIncArchivedRowKey(r store.ListUsersIncludingArchivedPageRow) (time.Time, pgtype.UUID) {
	return r.CreatedAt.Time, r.ID
}

func usersListRowToResponse(r store.ListUsersPageRow) userResponse {
	return toUserResponse(r.ID, r.Email, r.Name, r.IsAdmin, r.CreatedAt, pgtype.Timestamptz{})
}
func usersListIncArchivedRowToResponse(r store.ListUsersIncludingArchivedPageRow) userResponse {
	return toUserResponse(r.ID, r.Email, r.Name, r.IsAdmin, r.CreatedAt, r.ArchivedAt)
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	includeArchived := r.URL.Query().Get("include_archived") == "true"

	// ?email=<exact> branch — at most one row, but still wrapped in the envelope
	// for shape uniformity (so SPA clients parse one shape per endpoint).
	if email := r.URL.Query().Get("email"); email != "" {
		u, err := s.q.GetUserByEmailPublic(r.Context(), email)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusOK, page[userResponse]{Items: []userResponse{}, NextCursor: "", Total: 0})
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to look up user")
			return
		}
		if u.ArchivedAt.Valid && !includeArchived {
			writeJSON(w, http.StatusOK, page[userResponse]{Items: []userResponse{}, NextCursor: "", Total: 0})
			return
		}
		writeJSON(w, http.StatusOK, page[userResponse]{
			Items:      []userResponse{toUserResponse(u.ID, u.Email, u.Name, u.IsAdmin, u.CreatedAt, u.ArchivedAt)},
			NextCursor: "",
			Total:      1,
		})
		return
	}

	pp, ok := parsePage(w, r)
	if !ok {
		return
	}

	if includeArchived {
		rows, err := s.q.ListUsersIncludingArchivedPage(r.Context(), store.ListUsersIncludingArchivedPageParams{
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list users")
			return
		}
		total, err := s.q.CountUsersIncludingArchived(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to count users")
			return
		}
		items, next := buildPage(rows, pp.Limit, usersListIncArchivedRowToResponse, usersListIncArchivedRowKey)
		writeJSON(w, http.StatusOK, page[userResponse]{Items: items, NextCursor: next, Total: total})
		return
	}

	rows, err := s.q.ListUsersPage(r.Context(), store.ListUsersPageParams{
		CursorSet: pp.Cursor.Set,
		CursorTs:  pp.CursorTs(),
		CursorID:  pp.Cursor.ID,
		PageLimit: pp.Limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	total, err := s.q.CountUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count users")
		return
	}
	items, next := buildPage(rows, pp.Limit, usersListRowToResponse, usersListRowKey)
	writeJSON(w, http.StatusOK, page[userResponse]{Items: items, NextCursor: next, Total: total})
}
```

- [ ] **Step 2: Verify the users handler compiles**

```bash
go build ./internal/api/
```

Expected: `internal/api/users.go` compiles.

- [ ] **Step 3: Commit**

```bash
git add internal/api/users.go
git commit -m "feat(api): paginate /v1/users with envelope response"
```

---

### Task 9: `handleListScheduledJobs` envelope migration

**Files:**
- Modify: `internal/api/scheduled_jobs.go`

- [ ] **Step 1: Replace `handleListScheduledJobs`**

In `internal/api/scheduled_jobs.go`, replace the existing `handleListScheduledJobs` (lines ~170–192) with:

```go
import (
	// add to existing imports:
	"time"
)

func scheduledJobsRowKey(s store.ScheduledJob) (time.Time, pgtype.UUID) {
	return s.CreatedAt.Time, s.ID
}

func (s *Server) handleListScheduledJobs(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	pp, ok := parsePage(w, r)
	if !ok {
		return
	}

	if u.IsAdmin {
		rows, err := s.q.ListScheduledJobsPage(r.Context(), store.ListScheduledJobsPageParams{
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list scheduled jobs failed")
			return
		}
		total, err := s.q.CountScheduledJobs(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "count scheduled jobs failed")
			return
		}
		items, next := buildPage(rows, pp.Limit, toScheduledJobResponse, scheduledJobsRowKey)
		writeJSON(w, http.StatusOK, page[scheduledJobResponse]{Items: items, NextCursor: next, Total: total})
		return
	}

	rows, err := s.q.ListScheduledJobsByOwnerPage(r.Context(), store.ListScheduledJobsByOwnerPageParams{
		OwnerID:   u.ID,
		CursorSet: pp.Cursor.Set,
		CursorTs:  pp.CursorTs(),
		CursorID:  pp.Cursor.ID,
		PageLimit: pp.Limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list scheduled jobs failed")
		return
	}
	total, err := s.q.CountScheduledJobsByOwner(r.Context(), u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "count scheduled jobs failed")
		return
	}
	items, next := buildPage(rows, pp.Limit, toScheduledJobResponse, scheduledJobsRowKey)
	writeJSON(w, http.StatusOK, page[scheduledJobResponse]{Items: items, NextCursor: next, Total: total})
}
```

- [ ] **Step 2: Verify the scheduled-jobs handler compiles**

```bash
go build ./internal/api/
```

Expected: `internal/api/scheduled_jobs.go` compiles.

- [ ] **Step 3: Commit**

```bash
git add internal/api/scheduled_jobs.go
git commit -m "feat(api): paginate /v1/scheduled-jobs with envelope response"
```

---

### Task 10: `handleListAgentEnrollments` envelope migration

**Files:**
- Modify: `internal/api/agent_enrollments.go`

The existing handler at lines 76–96 builds an inline `map[string]any` per row (no named response struct) because `hostname_hint` is conditionally included. To preserve the exact wire shape, we keep `map[string]any` as the item type — `page[map[string]any]` works fine with generics.

- [ ] **Step 1: Replace `handleListAgentEnrollments`**

In `internal/api/agent_enrollments.go`, replace the function at lines 76–96 with:

```go
func enrollmentRowToMap(row store.ListActiveAgentEnrollmentsPageRow) map[string]any {
	entry := map[string]any{
		"id":         uuidStr(row.ID),
		"created_at": row.CreatedAt.Time,
		"expires_at": row.ExpiresAt.Time,
		"created_by": uuidStr(row.CreatedBy),
	}
	if row.HostnameHint != nil {
		entry["hostname_hint"] = *row.HostnameHint
	}
	return entry
}

func enrollmentRowKey(row store.ListActiveAgentEnrollmentsPageRow) (time.Time, pgtype.UUID) {
	return row.CreatedAt.Time, row.ID
}

func (s *Server) handleListAgentEnrollments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pp, ok := parsePage(w, r)
	if !ok {
		return
	}

	rows, err := s.q.ListActiveAgentEnrollmentsPage(ctx, store.ListActiveAgentEnrollmentsPageParams{
		CursorSet: pp.Cursor.Set,
		CursorTs:  pp.CursorTs(),
		CursorID:  pp.Cursor.ID,
		PageLimit: pp.Limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list enrollments")
		return
	}
	total, err := s.q.CountActiveAgentEnrollments(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count enrollments")
		return
	}
	items, next := buildPage(rows, pp.Limit, enrollmentRowToMap, enrollmentRowKey)
	writeJSON(w, http.StatusOK, page[map[string]any]{Items: items, NextCursor: next, Total: total})
}
```

The existing imports already include `time`, `pgtype`, and `store` — no import changes required.

- [ ] **Step 2: Verify the enrollments handler compiles**

```bash
go build ./internal/api/
```

Expected: `internal/api/agent_enrollments.go` compiles.

- [ ] **Step 3: Commit**

```bash
git add internal/api/agent_enrollments.go
git commit -m "feat(api): paginate /v1/agent-enrollments with envelope response"
```

---

### Task 11: `handleListReservations` envelope migration

**Files:**
- Modify: `internal/api/reservations.go`

The existing file already has `toReservationResponse(store.Reservation) reservationResponse` (line 25) — we just plug it into `buildPage`.

- [ ] **Step 1: Replace `handleListReservations`**

In `internal/api/reservations.go`, replace the function at lines 55–68 with:

```go
func reservationsRowKey(res store.Reservation) (time.Time, pgtype.UUID) {
	return res.CreatedAt.Time, res.ID
}

func (s *Server) handleListReservations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pp, ok := parsePage(w, r)
	if !ok {
		return
	}

	rows, err := s.q.ListReservationsPage(ctx, store.ListReservationsPageParams{
		CursorSet: pp.Cursor.Set,
		CursorTs:  pp.CursorTs(),
		CursorID:  pp.Cursor.ID,
		PageLimit: pp.Limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list reservations failed")
		return
	}
	total, err := s.q.CountReservations(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "count reservations failed")
		return
	}
	items, next := buildPage(rows, pp.Limit, toReservationResponse, reservationsRowKey)
	writeJSON(w, http.StatusOK, page[reservationResponse]{Items: items, NextCursor: next, Total: total})
}
```

The existing imports already include `time`, `pgtype`, and `store`.

- [ ] **Step 2: Verify the reservations handler compiles**

```bash
go build ./internal/api/
```

Expected: `internal/api/reservations.go` compiles.

- [ ] **Step 3: Verify the full project builds**

```bash
make build
```

Expected: all three binaries build successfully. The "broken state" introduced in Task 5 Step 8 is now resolved.

- [ ] **Step 4: Commit**

```bash
git add internal/api/reservations.go
git commit -m "feat(api): paginate /v1/reservations with envelope response"
```

---

## Phase 4 — API tests

### Task 12: Update existing API tests to envelope shape

**Files:**
- Modify: `internal/api/api_test.go`
- Modify: `internal/api/users_integration_test.go`
- Modify: `internal/api/scheduled_jobs_test.go`
- Modify: `internal/api/agent_enrollments_test.go`
- Modify: any other `internal/api/*_test.go` that decodes a list endpoint as a bare array

- [ ] **Step 1: Find all bare-array decode sites**

```bash
go test ./internal/api/ 2>&1 | head -60
```

Expected: failures like `json: cannot unmarshal object into Go value of type []*` at specific test lines. Each failure is one decode site to fix.

Also useful:

```bash
go test -tags integration -p 1 ./internal/api/ 2>&1 | head -60
```

Catches integration-only tests.

- [ ] **Step 2: Define a local envelope type for tests**

In `internal/api/api_test.go` (or wherever shared test helpers live), add:

```go
// pageEnvelope mirrors the API's response envelope so tests can decode list
// endpoints without depending on the api package's internal `page[T]` type.
type pageEnvelope[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor"`
	Total      int64  `json:"total"`
}
```

- [ ] **Step 3: Update each failing decode site**

For every site that currently does:

```go
var jobs []map[string]any
require.NoError(t, json.NewDecoder(rec.Body).Decode(&jobs))
// ... assertions on jobs
```

Change to:

```go
var resp pageEnvelope[map[string]any]
require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
// ... assertions on resp.Items, optionally check resp.Total
```

Repeat for the corresponding typed forms (`[]workerResponse`, `[]userResponse`, etc.) — the test file structure varies. The compile error or test failure points to each one.

For `internal/api/users_integration_test.go`:
- The `getUsers` helper used to fall back between bare-array and object shapes. Simplify to envelope-only.
- `TestListUsers_OrderedByCreatedAt` (or similarly-named): the previous expected order was ASC; flip the expected slice to DESC. Example: if expected was `[admin, alice, bob, carol]`, change to `[carol, bob, alice, admin]`.
- Tests that asserted `assert.NotNil(t, arr)` on an unmarshaled `nil` slice need to initialize with `arr := []map[string]any{}` (not `var arr []map[string]any`) before decoding, since Go unmarshals `[]` into a nil slice unless preallocated.

- [ ] **Step 4: Verify all API tests pass**

```bash
go test ./internal/api/ -timeout 120s
```

Expected: PASS (no failures).

```bash
go test -tags integration -p 1 ./internal/api/ -timeout 300s
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/
git commit -m "test(api): update list endpoint tests for envelope response shape"
```

---

### Task 13: Add deep integration tests for `/v1/jobs` pagination

**Files:**
- Create: `internal/api/jobs_pagination_test.go`

- [ ] **Step 1: Create the integration test file**

`internal/api/jobs_pagination_test.go`:

```go
//go:build integration

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pageEnvelope mirrors the API's response envelope so tests can decode list
// endpoints without depending on api package internals.
type pageEnvelope struct {
	Items      []map[string]any `json:"items"`
	NextCursor string           `json:"next_cursor"`
	Total      int64            `json:"total"`
}

func getJobsPage(t *testing.T, srv interface {
	Handler() http.Handler
}, token, query string) (int, pageEnvelope) {
	t.Helper()
	url := "/v1/jobs"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var resp pageEnvelope
	if rec.Code == http.StatusOK {
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	}
	return rec.Code, resp
}

func submitJob(t *testing.T, srv interface {
	Handler() http.Handler
}, token, name string) {
	t.Helper()
	body := fmt.Sprintf(`{"name":%q,"priority":"normal","tasks":[{"name":"t","command":["echo","x"]}]}`, name)
	req := httptest.NewRequest("POST", "/v1/jobs", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, "submit %s: %s", name, rec.Body.String())
}

func TestListJobs_PaginationDefaultLimit(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Alice", "page-alice@test.com", false)
	token := createTestToken(t, q, user.ID)

	// Insert 75 jobs. Sleep 1ms between each so created_at is unique
	// (cursor uses (created_at, id); identical timestamps work but we want
	// the test to exercise the timestamp comparator, not the id tiebreaker).
	for i := 0; i < 75; i++ {
		submitJob(t, srv, token, fmt.Sprintf("job-%02d", i))
		time.Sleep(time.Millisecond)
	}

	code, page1 := getJobsPage(t, srv, token, "")
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, page1.Items, 50, "default limit is 50")
	assert.NotEmpty(t, page1.NextCursor, "first page should signal more")
	assert.EqualValues(t, 75, page1.Total)

	code, page2 := getJobsPage(t, srv, token, "cursor="+page1.NextCursor)
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, page2.Items, 25, "second page has the remainder")
	assert.Empty(t, page2.NextCursor, "second page is the last")
	assert.EqualValues(t, 75, page2.Total)

	// Concatenated pages must be exactly the 75 jobs, no duplicates, no skips.
	seen := map[string]bool{}
	for _, j := range page1.Items {
		seen[j["id"].(string)] = true
	}
	for _, j := range page2.Items {
		id := j["id"].(string)
		assert.False(t, seen[id], "duplicate id across pages: %s", id)
		seen[id] = true
	}
	assert.Len(t, seen, 75)
}

func TestListJobs_StableUnderInsertMidPage(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Alice", "stable-alice@test.com", false)
	token := createTestToken(t, q, user.ID)

	for i := 0; i < 75; i++ {
		submitJob(t, srv, token, fmt.Sprintf("job-%02d", i))
		time.Sleep(time.Millisecond)
	}

	code, page1 := getJobsPage(t, srv, token, "")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, page1.Items, 50)
	require.NotEmpty(t, page1.NextCursor)

	// Insert a NEW job between page-1 and page-2. With cursor pagination over
	// (created_at DESC, id DESC), the new row arrives at the head of the feed
	// and must NOT bleed into page 2 — page 2 is bounded by the cursor.
	submitJob(t, srv, token, "interloper")

	code, page2 := getJobsPage(t, srv, token, "cursor="+page1.NextCursor)
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, page2.Items, 25, "page 2 must be stable: cursor bounds it to rows older than page 1")
	for _, j := range page2.Items {
		assert.NotEqual(t, "interloper", j["name"], "newly inserted row must not appear on page 2")
	}
}

func TestListJobs_LimitParam(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Alice", "limit-alice@test.com", false)
	token := createTestToken(t, q, user.ID)

	for i := 0; i < 5; i++ {
		submitJob(t, srv, token, fmt.Sprintf("job-%02d", i))
		time.Sleep(time.Millisecond)
	}

	code, p := getJobsPage(t, srv, token, "limit=3")
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, p.Items, 3)
	assert.NotEmpty(t, p.NextCursor)
	assert.EqualValues(t, 5, p.Total)
}

func TestListJobs_LimitOutOfRange(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Alice", "oor-alice@test.com", false)
	token := createTestToken(t, q, user.ID)

	cases := []string{"limit=0", "limit=201", "limit=-3", "limit=abc"}
	for _, qs := range cases {
		t.Run(qs, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/v1/jobs?"+qs, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
		})
	}
}

func TestListJobs_BadCursor(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Alice", "bad-alice@test.com", false)
	token := createTestToken(t, q, user.ID)

	req := httptest.NewRequest("GET", "/v1/jobs?cursor=garbage!!!", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid cursor")
}

func TestListJobs_EmptyResult(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Alice", "empty-alice@test.com", false)
	token := createTestToken(t, q, user.ID)

	code, p := getJobsPage(t, srv, token, "")
	require.Equal(t, http.StatusOK, code)
	assert.Empty(t, p.Items)
	assert.Empty(t, p.NextCursor, "empty result must yield empty cursor")
	assert.EqualValues(t, 0, p.Total)
}

func TestListJobs_StatusFilterPaginated(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Alice", "status-alice@test.com", false)
	token := createTestToken(t, q, user.ID)

	for i := 0; i < 3; i++ {
		submitJob(t, srv, token, fmt.Sprintf("job-%02d", i))
		time.Sleep(time.Millisecond)
	}

	// All three jobs are pending right after submit; status=running yields zero.
	code, p := getJobsPage(t, srv, token, "status=running")
	require.Equal(t, http.StatusOK, code)
	assert.Empty(t, p.Items)
	assert.EqualValues(t, 0, p.Total)

	code, p = getJobsPage(t, srv, token, "status=pending")
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, p.Items, 3)
	assert.EqualValues(t, 3, p.Total)
}
```

Note: `newTestServer`, `createTestUser`, `createTestToken` are existing test helpers in `internal/api`. Use them as-is. If their exact names differ, grep for their definitions in `internal/api/*_test.go` and adapt.

- [ ] **Step 2: Run the new integration tests**

```bash
go test -tags integration -p 1 ./internal/api/ -run TestListJobs -v -timeout 300s
```

Expected: PASS (7 tests).

- [ ] **Step 3: Run full integration suite to confirm no regressions**

```bash
make test-integration
```

Expected: all tests PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/api/jobs_pagination_test.go
git commit -m "test(api): add /v1/jobs pagination integration tests"
```

---

## Phase 5 — CLI

### Task 14: CLI `page.go` helper — `fetchAllPages`

**Files:**
- Create: `internal/cli/page.go`
- Create: `internal/cli/page_test.go`

- [ ] **Step 1: Write the failing test**

`internal/cli/page_test.go`:

```go
package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchAllPages_WalksTwoPages(t *testing.T) {
	type item struct {
		ID string `json:"id"`
	}

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "/v1/things", r.URL.Path)
		require.Equal(t, "200", r.URL.Query().Get("limit"))
		switch calls {
		case 1:
			require.Empty(t, r.URL.Query().Get("cursor"), "first call must have no cursor")
			json.NewEncoder(w).Encode(pageEnvelope[item]{
				Items:      []item{{ID: "a"}, {ID: "b"}},
				NextCursor: "next1",
				Total:      3,
			})
		case 2:
			require.Equal(t, "next1", r.URL.Query().Get("cursor"))
			json.NewEncoder(w).Encode(pageEnvelope[item]{
				Items:      []item{{ID: "c"}},
				NextCursor: "",
				Total:      3,
			})
		}
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "tok"}
	c := cfg.NewClient()
	got, total, err := fetchAllPages[item](context.Background(), c, "/v1/things", url.Values{}, 0)
	require.NoError(t, err)
	assert.Equal(t, []item{{ID: "a"}, {ID: "b"}, {ID: "c"}}, got)
	assert.EqualValues(t, 3, total)
	assert.Equal(t, 2, calls)
}

func TestFetchAllPages_RespectsUserLimit(t *testing.T) {
	type item struct {
		ID string `json:"id"`
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(pageEnvelope[item]{
			Items:      []item{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"}},
			NextCursor: "more",
			Total:      100,
		})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "tok"}
	c := cfg.NewClient()
	got, total, err := fetchAllPages[item](context.Background(), c, "/v1/things", url.Values{}, 3)
	require.NoError(t, err)
	assert.Len(t, got, 3, "userLimit=3 caps output at 3 even when more available")
	assert.EqualValues(t, 100, total)
}

func TestFetchAllPages_ForwardsParams(t *testing.T) {
	type item struct {
		ID string `json:"id"`
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "running", r.URL.Query().Get("status"))
		json.NewEncoder(w).Encode(pageEnvelope[item]{Total: 0})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "tok"}
	c := cfg.NewClient()
	params := url.Values{"status": []string{"running"}}
	_, _, err := fetchAllPages[item](context.Background(), c, "/v1/things", params, 0)
	require.NoError(t, err)
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/cli/ -run TestFetchAllPages -v
```

Expected: compile errors.

- [ ] **Step 3: Implement page.go**

`internal/cli/page.go`:

```go
package cli

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// pageEnvelope mirrors the server's pagination envelope.
type pageEnvelope[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor"`
	Total      int64  `json:"total"`
}

// pageRequestLimit is the per-request limit the CLI uses when auto-paginating.
// 200 matches the server's max so we minimize round-trips.
const pageRequestLimit = 200

// fetchAllPages walks ?cursor= until next_cursor is empty, or until userLimit
// rows have been collected (when userLimit > 0). Returns the merged slice and
// the total reported by the first page response. Caller-supplied params are
// forwarded on every page request alongside ?limit=200&cursor=<...>.
func fetchAllPages[T any](
	ctx context.Context,
	c *Client,
	basePath string,
	params url.Values,
	userLimit int,
) ([]T, int64, error) {
	if params == nil {
		params = url.Values{}
	}
	params.Set("limit", strconv.Itoa(pageRequestLimit))

	var (
		out    []T
		total  int64
		cursor string
		first  = true
	)
	for {
		if cursor != "" {
			params.Set("cursor", cursor)
		} else {
			params.Del("cursor")
		}
		path := basePath
		if encoded := params.Encode(); encoded != "" {
			path += "?" + encoded
		}
		var resp pageEnvelope[T]
		if err := c.do(ctx, "GET", path, nil, &resp); err != nil {
			return nil, 0, fmt.Errorf("paginate %s: %w", basePath, err)
		}
		if first {
			total = resp.Total
			first = false
		}
		out = append(out, resp.Items...)
		if userLimit > 0 && len(out) >= userLimit {
			return out[:userLimit], total, nil
		}
		if resp.NextCursor == "" {
			return out, total, nil
		}
		cursor = resp.NextCursor
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/cli/ -run TestFetchAllPages -v
```

Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/page.go internal/cli/page_test.go
git commit -m "feat(cli): add fetchAllPages helper for paginated list endpoints"
```

---

### Task 15: `relay jobs list` — auto-paginate + `--limit` + `Total:` header

**Files:**
- Modify: `internal/cli/jobs.go`

- [ ] **Step 1: Update `doListJobs`**

In `internal/cli/jobs.go`, replace the body of `doListJobs` with:

```go
func doListJobs(ctx context.Context, cfg *Config, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	status := fs.String("status", "", "filter by status")
	limitFlag := fs.Int("limit", 0, "cap output at N rows (0 = all)")
	asJSON := fs.Bool("json", false, "output raw JSON")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if cfg.Token == "" {
		return fmt.Errorf("no token configured — run 'relay login' first")
	}
	c := cfg.NewClient()

	params := url.Values{}
	if *status != "" {
		params.Set("status", *status)
	}
	jobs, total, err := fetchAllPages[jobResp](ctx, c, "/v1/jobs", params, *limitFlag)
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(w).Encode(jobs)
	}
	fmt.Fprintf(w, "Total: %d\n", total)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSTATUS\tSUBMITTED BY\tCREATED")
	// ... existing row-printing loop, unchanged
	// (keep whatever `for _, j := range jobs { fmt.Fprintf(tw, ...) }` block exists today)
	return tw.Flush()
}
```

Add `"net/url"` to the import block.

- [ ] **Step 2: Build and test**

```bash
go build ./internal/cli/
go test ./internal/cli/ -run TestJobsList -v
```

Expected: build succeeds. Existing CLI tests for jobs may fail — that's covered in Task 21.

- [ ] **Step 3: Commit**

```bash
git add internal/cli/jobs.go
git commit -m "feat(cli): auto-paginate relay jobs list with --limit flag"
```

---

### Task 16: `relay workers list` + `resolveWorkerID` switch

**Files:**
- Modify: `internal/cli/workers.go`

- [ ] **Step 1: Update `doWorkersList`**

In `internal/cli/workers.go`, replace `doWorkersList` with:

```go
func doWorkersList(ctx context.Context, c *Client, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("workers list", flag.ContinueOnError)
	limitFlag := fs.Int("limit", 0, "cap output at N rows (0 = all)")
	asJSON := fs.Bool("json", false, "output raw JSON")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	workers, total, err := fetchAllPages[workerResp](ctx, c, "/v1/workers", url.Values{}, *limitFlag)
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(w).Encode(workers)
	}
	fmt.Fprintf(w, "Total: %d\n", total)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSTATUS\tCPU\tRAM GB\tGPUS\tGPU MODEL")
	for _, wk := range workers {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\t%s\n",
			wk.ID, wk.Name, wk.Status, wk.CpuCores, wk.RamGb, wk.GpuCount, wk.GpuModel)
	}
	return tw.Flush()
}
```

- [ ] **Step 2: Update `resolveWorkerID`**

`resolveWorkerID` (used by `relay workers revoke <hostname>` and similar) currently calls `c.do(ctx, "GET", "/v1/workers", nil, &workers)` and parses a bare array. Change it to use `fetchAllPages`:

```go
func resolveWorkerID(ctx context.Context, c *Client, idOrHostname string) (string, error) {
	if isUUID(idOrHostname) {
		return idOrHostname, nil
	}
	workers, _, err := fetchAllPages[workerResp](ctx, c, "/v1/workers", url.Values{}, 0)
	if err != nil {
		return "", err
	}
	for _, wk := range workers {
		if wk.Hostname == idOrHostname {
			return wk.ID, nil
		}
	}
	return "", fmt.Errorf("no worker found with hostname %q", idOrHostname)
}
```

(The exact name `resolveWorkerID` and the precise function shape may vary slightly — read the existing file and adapt while preserving the existing `isUUID(...)` short-circuit and error-message semantics.)

Add `"net/url"` to imports.

- [ ] **Step 3: Build**

```bash
go build ./internal/cli/
```

Expected: build succeeds.

- [ ] **Step 4: Commit**

```bash
git add internal/cli/workers.go
git commit -m "feat(cli): auto-paginate relay workers list and resolveWorkerID"
```

---

### Task 17: `relay schedules list`

**Files:**
- Modify: `internal/cli/schedules.go`

The current `doSchedulesList` (lines 62–77) takes no flags. We add `--limit`, `--json`, switch to `fetchAllPages`, preserve the existing tabwriter row loop.

- [ ] **Step 1: Update imports and `doSchedulesList`**

Add `"net/url"` to the import block. Replace `doSchedulesList` (currently `func ... _ []string ...`) with:

```go
func doSchedulesList(ctx context.Context, c *Client, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("schedules list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limitFlag := fs.Int("limit", 0, "cap output at N rows (0 = all)")
	asJSON := fs.Bool("json", false, "output raw JSON")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	scheds, total, err := fetchAllPages[scheduleResp](ctx, c, "/v1/scheduled-jobs", url.Values{}, *limitFlag)
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(w).Encode(scheds)
	}
	fmt.Fprintf(w, "Total: %d\n", total)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tCRON\tTZ\tENABLED\tNEXT")
	for _, s := range scheds {
		next := ""
		if s.NextRunAt != nil {
			next = s.NextRunAt.Format("2006-01-02 15:04")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%t\t%s\n", s.ID, s.Name, s.CronExpr, s.Timezone, s.Enabled, next)
	}
	return tw.Flush()
}
```

- [ ] **Step 2: Build**

```bash
go build ./internal/cli/
```

- [ ] **Step 3: Commit**

```bash
git add internal/cli/schedules.go
git commit -m "feat(cli): auto-paginate relay schedules list"
```

---

### Task 18: `relay reservations list`

**Files:**
- Modify: `internal/cli/reservations.go`

The current `doReservationsList` (lines 55–77) has no flag parsing. Add `--limit` and `--json`. Add `"flag"` and `"net/url"` to imports.

- [ ] **Step 1: Update imports and `doReservationsList`**

Add to the import block (currently missing both): `"flag"`, `"net/url"`.

Replace `doReservationsList` with:

```go
func doReservationsList(ctx context.Context, c *Client, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("reservations list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limitFlag := fs.Int("limit", 0, "cap output at N rows (0 = all)")
	asJSON := fs.Bool("json", false, "output raw JSON")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	reservations, total, err := fetchAllPages[reservationResp](ctx, c, "/v1/reservations", url.Values{}, *limitFlag)
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(w).Encode(reservations)
	}
	fmt.Fprintf(w, "Total: %d\n", total)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tPROJECT\tSTARTS\tENDS")
	for _, res := range reservations {
		project := ""
		if res.Project != nil {
			project = *res.Project
		}
		starts, ends := "", ""
		if res.StartsAt != nil {
			starts = res.StartsAt.Format("2006-01-02")
		}
		if res.EndsAt != nil {
			ends = res.EndsAt.Format("2006-01-02")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", res.ID, res.Name, project, starts, ends)
	}
	return tw.Flush()
}
```

- [ ] **Step 2: Build**

```bash
go build ./internal/cli/
```

- [ ] **Step 3: Commit**

```bash
git add internal/cli/reservations.go
git commit -m "feat(cli): auto-paginate relay reservations list"
```

---

### Task 19: `relay admin users list` + email-lookup unwraps

**Files:**
- Modify: `internal/cli/admin_users.go`

The current `doAdminUsersList` (lines 45–71) uses `flag.NewFlagSet` with `--include-archived`. Two other call sites (`doAdminUsersGet` line ~110, `doAdminUsersUpdate` line ~166) decode `?email=` lookups as `[]userListItem`.

- [ ] **Step 1: Update `doAdminUsersList`**

Replace the function body with:

```go
func doAdminUsersList(ctx context.Context, cfg *Config, args []string, out io.Writer) error {
	if cfg.Token == "" {
		return fmt.Errorf("not logged in — run 'relay login' first")
	}

	fs := flag.NewFlagSet("admin users list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	includeArchived := fs.Bool("include-archived", false, "include archived users in the list")
	limitFlag := fs.Int("limit", 0, "cap output at N rows (0 = all)")
	asJSON := fs.Bool("json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("usage: relay admin users list [--include-archived] [--limit N] [--json]")
	}

	c := cfg.NewClient()
	params := url.Values{}
	if *includeArchived {
		params.Set("include_archived", "true")
	}
	users, total, err := fetchAllPages[userListItem](ctx, c, "/v1/users", params, *limitFlag)
	if err != nil {
		return fmt.Errorf("list users: %w", err)
	}
	if *asJSON {
		return json.NewEncoder(out).Encode(users)
	}
	fmt.Fprintf(out, "Total: %d\n", total)
	printUsersTable(out, users, *includeArchived)
	return nil
}
```

Add `"encoding/json"` to imports if not already present.

- [ ] **Step 2: Update `doAdminUsersGet` email-lookup branch**

In `doAdminUsersGet` (around lines 100–120), the call:

```go
var users []userListItem
path := "/v1/users?email=" + url.QueryEscape(email)
if err := c.do(ctx, "GET", path, nil, &users); err != nil {
    return fmt.Errorf("get user: %w", err)
}
if len(users) == 0 {
    return fmt.Errorf("user not found: %s", email)
}
printUserDetail(out, users[0])
```

Becomes:

```go
var resp pageEnvelope[userListItem]
path := "/v1/users?email=" + url.QueryEscape(email)
if err := c.do(ctx, "GET", path, nil, &resp); err != nil {
    return fmt.Errorf("get user: %w", err)
}
if len(resp.Items) == 0 {
    return fmt.Errorf("user not found: %s", email)
}
printUserDetail(out, resp.Items[0])
```

- [ ] **Step 3: Update `doAdminUsersUpdate` email-lookup branch**

In `doAdminUsersUpdate` (around lines 165–175), the same pattern:

```go
var users []userListItem
path := "/v1/users?email=" + url.QueryEscape(target)
if err := c.do(ctx, "GET", path, nil, &users); err != nil {
    return fmt.Errorf("look up user: %w", err)
}
if len(users) == 0 {
    return fmt.Errorf("user not found: %s", target)
}
id = users[0].ID
```

Becomes:

```go
var resp pageEnvelope[userListItem]
path := "/v1/users?email=" + url.QueryEscape(target)
if err := c.do(ctx, "GET", path, nil, &resp); err != nil {
    return fmt.Errorf("look up user: %w", err)
}
if len(resp.Items) == 0 {
    return fmt.Errorf("user not found: %s", target)
}
id = resp.Items[0].ID
```

- [ ] **Step 4: Check for archive/unarchive email-lookup branches**

```bash
grep -n "var users \[\]userListItem" internal/cli/admin_users.go
```

If there are remaining matches in `doAdminUsersArchive` or `doAdminUsersUnarchive`, apply the same transformation. The functions resolve email→ID before issuing PATCH/POST archive operations.

- [ ] **Step 5: Build**

```bash
go build ./internal/cli/
```

Expected: builds cleanly.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/admin_users.go
git commit -m "feat(cli): auto-paginate relay admin users list and unwrap email lookups"
```

---

### Task 20: `relay admin enroll list` (if equivalent exists)

**Files:**
- Modify: `internal/cli/agent_enroll.go` (or whichever file owns the enrollment subcommands)

- [ ] **Step 1: Locate the list subcommand**

```bash
grep -rn "agent_enrollments\|enroll list\|/v1/agent-enrollments" internal/cli/
```

This identifies the file and function name owning the enrollments list. There may be no human-facing list subcommand in the CLI today (only enroll/consume). If so, skip this task — the spec lists it as conditional (`relay admin enroll list (or equivalent)`).

- [ ] **Step 2 (if a list command exists): Apply the standard pattern**

Same as Task 17 / Task 18: `flag.NewFlagSet`, `--limit`, `--json`, `fetchAllPages[<respType>]`, `Total:` header.

- [ ] **Step 3: Build**

```bash
go build ./internal/cli/
```

- [ ] **Step 4: Commit (if changes were made)**

```bash
git add internal/cli/agent_enroll.go
git commit -m "feat(cli): auto-paginate relay admin enroll list"
```

If no list command existed and this task was skipped, commit a single-line note in the plan execution log instead.

---

## Phase 6 — CLI tests + docs

### Task 21: Update CLI tests to envelope mock servers

**Files:**
- Modify: `internal/cli/jobs_test.go`
- Modify: `internal/cli/workers_test.go`
- Modify: `internal/cli/workers_revoke_test.go`
- Modify: `internal/cli/workers_workspaces_test.go`
- Modify: `internal/cli/schedules_test.go`
- Modify: `internal/cli/reservations_test.go`
- Modify: `internal/cli/admin_users_test.go`

- [ ] **Step 1: Find all failing CLI tests**

```bash
go test ./internal/cli/ -timeout 60s 2>&1 | head -80
```

Expected: failures from tests whose mock servers return bare arrays. The error pattern is `json: cannot unmarshal array into Go value of type cli.pageEnvelope[...]`.

- [ ] **Step 2: Update each mock server**

For every test mock server that returns a list, change:

```go
json.NewEncoder(w).Encode([]workerResp{{ID: "...", Hostname: "..."}})
```

to:

```go
json.NewEncoder(w).Encode(pageEnvelope[workerResp]{
    Items: []workerResp{{ID: "...", Hostname: "..."}},
    Total: 1,
})
```

Apply per-file. Use grep to find them quickly:

```bash
grep -rn "json.NewEncoder(w).Encode(\[\]" internal/cli/
```

- [ ] **Step 3: Update RawQuery assertions**

Tests that asserted `assert.Empty(t, r.URL.RawQuery)` will fail because auto-paginate now appends `?limit=200`. Change to:

```go
require.Equal(t, "limit=200", r.URL.RawQuery)
```

For tests that pass a filter (e.g., `?include_archived=true`), the assertion should match both params:

```go
assert.Contains(t, r.URL.RawQuery, "limit=200")
assert.Contains(t, r.URL.RawQuery, "include_archived=true")
```

The exact assertions depend on how each test was written. Grep for `RawQuery` to find them all:

```bash
grep -rn "RawQuery" internal/cli/
```

- [ ] **Step 4: Run all CLI tests**

```bash
go test ./internal/cli/ -timeout 120s
```

Expected: PASS (no failures).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/
git commit -m "test(cli): update mock servers for envelope shape and limit query param"
```

---

### Task 22: README — Pagination section + endpoint annotations

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Read the existing API reference section**

```bash
grep -n "## API\|GET /v1/jobs\|/v1/workers\|## CLI" README.md | head -30
```

Identify where the API reference table or list lives, and where to insert a new "Pagination" subsection.

- [ ] **Step 2: Add the Pagination subsection**

Insert under the API reference top-level heading, before the per-endpoint tables:

```markdown
### Pagination

All list endpoints (`/v1/jobs`, `/v1/workers`, `/v1/users`, `/v1/scheduled-jobs`, `/v1/agent-enrollments`, `/v1/reservations`) return a wrapped JSON envelope:

```json
{
  "items": [ ... ],
  "next_cursor": "eyJ0IjoiMjAyNi0wNS0wNlQxNToz...",
  "total": 274
}
```

**Query parameters:**
- `limit` — page size, default 50, maximum 200. Out-of-range values return `400 invalid limit`.
- `cursor` — opaque cursor returned by the previous page's `next_cursor`. Empty/absent for the first page. Malformed cursor returns `400 invalid cursor`.

**Sort order:** `created_at DESC, id DESC` for every list endpoint.

**Cursor opacity:** Clients MUST treat `next_cursor` as opaque. Its format is server-internal and may change without notice.

**`total`** reflects the count of rows matching any active filters (`?status=`, `?email=`, etc.).
```

- [ ] **Step 3: Annotate the API table**

In the existing endpoint table or list, mark each of the six list endpoints as **Paginated**. Add a note about the two ordering changes:

```markdown
> **Behavior change (2026-05-06):** `/v1/workers` now sorts by `created_at DESC` (was `name`). `/v1/users` now sorts by `created_at DESC` (was `created_at ASC`).
```

- [ ] **Step 4: Verify the markdown renders**

Skim the modified section in a markdown viewer or look at the diff to make sure tables and code blocks are well-formed.

```bash
git diff README.md
```

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "docs(api): add pagination section and annotate list endpoints"
```

---

### Task 23: Backlog entries — filters + custom sort

**Files:**
- Create: `docs/backlog/idea-2026-05-06-list-endpoint-filters.md`
- Create: `docs/backlog/idea-2026-05-06-list-endpoint-sort.md`

- [ ] **Step 1: Create filters backlog entry**

`docs/backlog/idea-2026-05-06-list-endpoint-filters.md`:

```markdown
---
title: Filter improvements for paginated list endpoints
type: idea
status: open
created: 2026-05-06
source: pagination brainstorming session (2026-05-06)
---

# Filter improvements for paginated list endpoints

## Summary
The 2026-05-06 pagination work added cursor-based pagination across all six unbounded list endpoints but explicitly deferred filter expansion. This entry captures the deferred filter work surfaced during brainstorming so it isn't lost when the SPA UI calls for richer filtering.

## Context
Today the list endpoints have minimal filters:
- `/v1/jobs`: `?status=<single>` and `?scheduled_job_id=<uuid>`
- `/v1/users`: `?email=<exact>` and `?include_archived=true`
- All others: no filters

Adding more filters during the pagination change would have made the diff hard to review. They're independent of the cursor mechanism (the cursor key stays `(created_at, id)` regardless of which `WHERE` clauses scope the result set), so they can ship as a focused follow-up.

## Proposal
Likely useful additions, in rough priority order:

1. **Multi-value status on `/v1/jobs`** — `?status=running,queued`. SPA dashboards typically want "active jobs" which spans multiple statuses.
2. **`?submitted_by=<user_id_or_email>` on `/v1/jobs`** — "my jobs" view in the UI.
3. **Time-range filters** — `?since=<ts>` / `?until=<ts>` on `/v1/jobs` for "last 24 hours" tabs.
4. **`?enabled=true` on `/v1/scheduled-jobs`** — show only enabled schedules.
5. **`?status=online|offline` on `/v1/workers`** — "agents online" view without client-side filtering.
6. **Label filtering** — `?label.team=infra` (JSONB containment via `labels @> '{"team":"infra"}'::jsonb`). Useful but heavier — needs a GIN index on `labels`.
7. **Name substring search** — `?q=<substring>` ILIKE on `name`. Needs a `pg_trgm` index for performance at scale.

## Cursor compatibility
All of the above keep ordering by `(created_at DESC, id DESC)`, so the existing cursor scheme works unchanged. Filters that *change ordering* — e.g. relevance-ranked search on `q` with a `ts_rank()` score — would need a different cursor scheme; not a concern for any item in this list.

## Related
- `internal/api/jobs.go` (`handleListJobs`) — current filter dispatch
- `internal/store/query/jobs.sql` — sqlc queries to extend
- `internal/store/migrations/000011_pagination_indexes.up.sql` — composite index file new filter indexes would join
- `docs/superpowers/specs/2026-05-06-list-endpoint-pagination-design.md` — pagination spec
```

- [ ] **Step 2: Create sort backlog entry**

`docs/backlog/idea-2026-05-06-list-endpoint-sort.md`:

```markdown
---
title: Custom sort order for paginated list endpoints
type: idea
status: open
created: 2026-05-06
source: pagination brainstorming session (2026-05-06)
---

# Custom sort order for paginated list endpoints

## Summary
The 2026-05-06 pagination work shipped with a fixed sort order of `created_at DESC, id DESC` across all paginated list endpoints. The cursor format was deliberately designed to be forward-compatible with custom sort orders, but the per-endpoint sort whitelist, cursor codec extension, and additional indexes were deferred. This entry captures what's needed when the SPA UI introduces sort-by-name dropdowns or similar.

## Context
The current cursor encodes `(created_at, id)`. Sorting by a different field — e.g. `name` for jobs, `hostname` for workers — requires the cursor to encode the new sort key plus the `id` tiebreaker, and the `WHERE` clause to compare against that key.

## Proposal

### API surface
```
GET /v1/jobs?sort=name&order=asc&limit=50&cursor=<...>
```

Per-endpoint sort whitelist (allowed `sort=` values):
- `/v1/jobs`: `created_at` (default), `name`, `status`
- `/v1/workers`: `created_at` (default), `hostname`, `name`
- `/v1/users`: `created_at` (default), `email`, `name`
- `/v1/scheduled-jobs`: `created_at` (default), `name`, `next_run_at`
- `/v1/reservations`: `created_at` (default), `name`
- `/v1/agent-enrollments`: `created_at` (default)

`order=asc|desc` defaults to `desc`. Whitelisting prevents SQL injection via `?sort=arbitrary_column`.

### Cursor codec extension
The cursor JSON gets a `k` (key name) and `v` (value) field:
```json
{"k":"name","v":"render-job-42","i":"<uuid>"}
```

Old cursors without `k` decode cleanly: missing `k` defaults to `created_at`, the `v` field is interpreted as the timestamp string. Existing clients holding old cursors continue working without change.

### Indexes
One composite index per allowed sort field, e.g.:
- `CREATE INDEX idx_jobs_name_id ON jobs(name ASC, id DESC);`
- `CREATE INDEX idx_jobs_status_id ON jobs(status ASC, id DESC);`

### Query shape
```sql
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (name, id) < (sqlc.arg(cursor_v)::text, sqlc.arg(cursor_id)::uuid))
ORDER BY name ASC, id DESC
LIMIT $n + 1
```

A separate sqlc query per (endpoint × sort field) combination, or one query per endpoint with conditional `ORDER BY` in Go-side template. Probably cleanest is one query per (endpoint × sort field).

## Risks
- Cursors persisted by clients before this change still work (forward-compatible).
- Cursors persisted after this change must NOT be used against an endpoint that doesn't support that sort key — server returns `400 invalid cursor`.
- More indexes increase write cost slightly. Acceptable given Relay's write rate.

## Related
- `docs/superpowers/specs/2026-05-06-list-endpoint-pagination-design.md` — pagination spec
- `internal/api/pagination.go` — cursor codec, would extend `cursorWire` with `K` and `V` fields
```

- [ ] **Step 3: Verify both files exist**

```bash
ls -la docs/backlog/idea-2026-05-06-*.md
```

Expected: two files listed.

- [ ] **Step 4: Commit**

```bash
git add docs/backlog/idea-2026-05-06-list-endpoint-filters.md docs/backlog/idea-2026-05-06-list-endpoint-sort.md
git commit -m "backlog: capture deferred pagination filter and sort work"
```

---

## Final verification

### Task 24: End-to-end verification

- [ ] **Step 1: Full unit test pass**

```bash
make test
```

Expected: all unit tests PASS, no skipped tests, no panics.

- [ ] **Step 2: Full integration test pass**

```bash
make test-integration
```

Expected: all integration tests PASS. Pay attention to:
- `internal/api/jobs_pagination_test.go` (Task 13 — 7 tests)
- `internal/api/users_integration_test.go` (Task 12 — ordering change)
- `internal/cli/*_test.go` (Task 21 — envelope mocks)
- `internal/schedrunner/runner_test.go` (must still pass — uses kept `ListJobsByScheduledJob`)
- `internal/scheduler/dispatch_test.go` (must still pass — uses kept `ListWorkers`, `ListActiveReservations`)
- `internal/store/agent_enrollments_test.go` (must still pass — uses kept `ListActiveAgentEnrollments`)

- [ ] **Step 3: End-to-end smoke test (manual)**

Run a server against a local Postgres with seed data:

```bash
make build
# In one terminal:
bin/relay-server
# In another terminal — assuming a token exists in ~/.relay/config.json:
for i in $(seq 1 75); do bin/relay jobs submit --name "smoke-$i" --command echo,hello; done
curl -s -H "Authorization: Bearer $TOKEN" 'http://localhost:8080/v1/jobs' | jq '. | {items_count: (.items | length), next_cursor, total}'
# Expected: items_count=50, next_cursor non-empty, total=75

CURSOR=$(curl -s -H "Authorization: Bearer $TOKEN" 'http://localhost:8080/v1/jobs' | jq -r .next_cursor)
curl -s -H "Authorization: Bearer $TOKEN" "http://localhost:8080/v1/jobs?cursor=$CURSOR" | jq '. | {items_count: (.items | length), next_cursor, total}'
# Expected: items_count=25, next_cursor empty, total=75

bin/relay jobs list | head -5
# Expected: "Total: 75" header line, then table

bin/relay jobs list --limit 10 | wc -l
# Expected: 12 lines (header + 1 column header + 10 rows)
```

If running on Windows, swap shell idioms accordingly (PowerShell `for` loop, etc.). The test is informational — failures here usually indicate missing migrations or stale binaries.

- [ ] **Step 4: Final commit (if any verification fixes were needed)**

If smoke testing surfaced issues, commit fixes. Otherwise, this task has no commit.

---

## Plan execution checklist

When done, the working tree should contain:

- ✅ `internal/api/pagination.go` and `internal/api/pagination_test.go` — codec + parser + buildPage
- ✅ `internal/store/migrations/000011_pagination_indexes.{up,down}.sql`
- ✅ All 6 `internal/store/query/*.sql` files updated
- ✅ All 6 `internal/api/<resource>.go` handlers paginated
- ✅ `internal/api/jobs_pagination_test.go` — 7 deep integration tests
- ✅ `internal/cli/page.go` and `internal/cli/page_test.go` — auto-paginate helper
- ✅ All 6 CLI list commands updated with `--limit` and `Total:` header
- ✅ `README.md` — Pagination section + endpoint annotations
- ✅ `docs/backlog/idea-2026-05-06-list-endpoint-filters.md`
- ✅ `docs/backlog/idea-2026-05-06-list-endpoint-sort.md`
- ✅ `make test` and `make test-integration` both green
