# Web Jobs List page (Table view) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the Jobs list Table view in the Relay web frontend, backed by an enriched `GET /v1/jobs` (task progress, timing, schedule source) and a new `GET /v1/jobs/stats` aggregate for the KPI strip.

**Architecture:** Backend adds a per-row `LEFT JOIN LATERAL` over `tasks` plus a `LEFT JOIN scheduled_jobs` to all 12 paginated list queries, surfacing new fields on `jobResponse`; a new `JobStatusCounts` aggregate powers a `handleJobStats` endpoint. Frontend adds a `web/src/jobs/` feature mirroring `web/src/workers/`: typed API client, react-query polling hooks, a status/format helper module, a presentational table, and a page composing the KPI strip, status filter chips, a sort dropdown, and cursor pagination.

**Tech Stack:** Go, sqlc, pgx, Postgres (backend); React 18, Vite, TypeScript, @tanstack/react-query, Tailwind, Vitest, MSW (frontend).

**Spec:** `docs/superpowers/specs/2026-06-05-web-jobs-list-design.md`

---

## File Structure

**Backend (create/modify):**
- Modify: `internal/store/query/jobs.sql` - add enrichment to 12 list queries; add `JobStatusCounts`.
- Regenerate: `internal/store/jobs.sql.go`, `internal/store/models.go` (via `make generate` - never hand-edit).
- Modify: `internal/api/jobs.go` - new `jobResponse` fields, `applyJobEnrichment` helper, update 12 `jobRowToResponse*`, add `jobStatsResponse` + `handleJobStats`.
- Modify: `internal/api/server.go` - register `GET /v1/jobs/stats`.
- Create: `internal/api/jobs_enrichment_integration_test.go` - asserts task counts, timing, schedule name on list rows.
- Create: `internal/api/jobs_stats_integration_test.go` - asserts stats buckets and the 24h window.

**Frontend (create/modify):**
- Create: `web/src/jobs/api.ts` (+ `api.test.ts`) - types and fetchers.
- Create: `web/src/jobs/status.ts` (+ `status.test.ts`) - status colors, duration/started/progress formatting.
- Create: `web/src/jobs/useJobs.ts` (+ `useJobs.test.tsx`) - list polling hook.
- Create: `web/src/jobs/useJobStats.ts` (+ `useJobStats.test.tsx`) - stats polling hook.
- Create: `web/src/jobs/JobsTable.tsx` (+ `JobsTable.test.tsx`) - presentational table.
- Create: `web/src/jobs/SortControl.tsx` - dropdown sort selector.
- Create: `web/src/jobs/JobsPage.tsx` (+ `JobsPage.test.tsx`) - page composition.
- Modify: `web/src/app/router.tsx` - route `/jobs` to `JobsPage`. `JobsPlaceholder` stays (still used by `/schedules`, `/admin`, `/profile/*`).

---

## Backend

### Task 1: Enrich the jobs list SQL queries

**Files:**
- Modify: `internal/store/query/jobs.sql`

- [ ] **Step 1: Add the enrichment to the default `ListJobsWithEmailPage` query**

In `internal/store/query/jobs.sql`, replace the `ListJobsWithEmailPage` query (lines ~15-22) with:

```sql
-- name: ListJobsWithEmailPage :many
SELECT j.*, u.email AS submitted_by_email,
       ts.total_tasks, ts.done_tasks, ts.started_at, ts.finished_at,
       sj.name AS scheduled_job_name
FROM jobs j
JOIN users u ON u.id = j.submitted_by
LEFT JOIN LATERAL (
  SELECT COUNT(*)                                  AS total_tasks,
         COUNT(*) FILTER (WHERE t.status = 'done') AS done_tasks,
         MIN(t.started_at)::timestamptz            AS started_at,
         MAX(t.finished_at)::timestamptz           AS finished_at
  FROM tasks t WHERE t.job_id = j.id
) ts ON TRUE
LEFT JOIN scheduled_jobs sj ON sj.id = j.scheduled_job_id
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (j.created_at, j.id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY j.created_at DESC, j.id DESC
LIMIT sqlc.arg(page_limit)::int + 1;
```

- [ ] **Step 2: Apply the same two joins + three SELECT additions to the other 11 list queries**

For each query below, insert these two lines immediately after its `JOIN users u ON u.id = j.submitted_by` line:

```sql
LEFT JOIN LATERAL (
  SELECT COUNT(*)                                  AS total_tasks,
         COUNT(*) FILTER (WHERE t.status = 'done') AS done_tasks,
         MIN(t.started_at)::timestamptz            AS started_at,
         MAX(t.finished_at)::timestamptz           AS finished_at
  FROM tasks t WHERE t.job_id = j.id
) ts ON TRUE
LEFT JOIN scheduled_jobs sj ON sj.id = j.scheduled_job_id
```

and change each query's `SELECT j.*, u.email AS submitted_by_email` to:

```sql
SELECT j.*, u.email AS submitted_by_email,
       ts.total_tasks, ts.done_tasks, ts.started_at, ts.finished_at,
       sj.name AS scheduled_job_name
```

Apply to all of: `ListJobsByStatusWithEmailPage`, `ListJobsByScheduledJobWithEmailPage`, `ListJobsWithEmailPageByCreatedAsc`, `ListJobsWithEmailPageByNameDesc`, `ListJobsWithEmailPageByNameAsc`, `ListJobsWithEmailPageByPriorityDesc`, `ListJobsWithEmailPageByPriorityAsc`, `ListJobsWithEmailPageByStatusDesc`, `ListJobsWithEmailPageByStatusAsc`, `ListJobsWithEmailPageByUpdatedDesc`, `ListJobsWithEmailPageByUpdatedAsc`.

Do NOT modify `GetJobWithEmail`, `ListJobsByScheduledJob` (internal test helper), `CountJobs`, `CountJobsByStatus`, or `CountJobsByScheduledJob`. The `WHERE`/`ORDER BY`/`LIMIT` clauses of each list query are unchanged - only the `SELECT` and the two joins are added. Because the `LATERAL` is a scalar aggregate and `sj` joins one-to-one on the PK, no query produces extra rows.

- [ ] **Step 3: Add the stats aggregate query**

Append to `internal/store/query/jobs.sql`:

```sql
-- name: JobStatusCounts :one
-- Fleet-wide job counts for the dashboard KPI strip. running/queued are current
-- totals; done_24h/failed_24h are windowed on updated_at, which is a faithful
-- finish-time proxy because the only writer of updated_at is UpdateJobStatus and
-- a terminal state is the last transition a job makes (see the design spec).
SELECT
  COUNT(*) FILTER (WHERE status IN ('running','dispatched'))                                              AS running,
  COUNT(*) FILTER (WHERE status IN ('queued','pending'))                                                  AS queued,
  COUNT(*) FILTER (WHERE status = 'done'                  AND updated_at >= NOW() - INTERVAL '24 hours')  AS done_24h,
  COUNT(*) FILTER (WHERE status IN ('failed','timed_out') AND updated_at >= NOW() - INTERVAL '24 hours')  AS failed_24h
FROM jobs;
```

- [ ] **Step 4: Regenerate the store layer**

Run: `make generate`
Expected: `internal/store/jobs.sql.go` (and possibly `models.go`) regenerate with no errors. The generated `ListJobsWithEmailPageRow` and siblings now carry `TotalTasks int64`, `DoneTasks int64`, `StartedAt pgtype.Timestamptz`, `FinishedAt pgtype.Timestamptz`, `ScheduledJobName *string` (sqlc emits `*string` for the nullable `sj.name`). The `::timestamptz` casts are required so sqlc infers `pgtype.Timestamptz` for the MIN/MAX rather than `interface{}`. A new `JobStatusCountsRow` has `Running`, `Queued`, `Done24h`, `Failed24h` (all `int64`).

Note (Windows): `sqlc generate` may rewrite `internal/store/*.sql.go` with line-ending-only changes. Restrict the commit to the intended files: `git add internal/store/query/jobs.sql internal/store/jobs.sql.go internal/store/models.go` and run `git checkout -- internal/store/` for any other `*.sql.go` showing an empty diff.

- [ ] **Step 5: Verify the project still builds**

Run: `go build ./...`
Expected: PASS. The regenerated row structs gain new fields, but unused struct fields are legal in Go, so the existing `jobRowToResponse*` functions still compile (they just don't read the new fields yet - Task 2 wires them in). If the build fails in `internal/store`, the SQL is malformed - fix before continuing.

- [ ] **Step 6: Commit**

```bash
git add internal/store/query/jobs.sql internal/store/jobs.sql.go internal/store/models.go
git commit -m "feat(store): enrich jobs list queries with task progress, timing, schedule name; add JobStatusCounts"
```

---

### Task 2: Surface enrichment fields on jobResponse

**Files:**
- Modify: `internal/api/jobs.go`

- [ ] **Step 1: Add the new fields to `jobResponse`**

In `internal/api/jobs.go`, replace the `jobResponse` struct (lines ~54-65) with:

```go
type jobResponse struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Priority         string          `json:"priority"`
	Status           string          `json:"status"`
	SubmittedBy      string          `json:"submitted_by"`
	SubmittedByEmail string          `json:"submitted_by_email,omitempty"`
	Labels           json.RawMessage `json:"labels"`
	Tasks            []taskResponse  `json:"tasks,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`

	// Enrichment populated only on list rows (GET /v1/jobs). Derived from the
	// job's tasks and its scheduled-job source.
	TotalTasks       int32           `json:"total_tasks,omitempty"`
	DoneTasks        int32           `json:"done_tasks,omitempty"`
	StartedAt        *time.Time      `json:"started_at,omitempty"`
	FinishedAt       *time.Time      `json:"finished_at,omitempty"`
	ScheduledJobID   string          `json:"scheduled_job_id,omitempty"`
	ScheduledJobName string          `json:"scheduled_job_name,omitempty"`
}
```

- [ ] **Step 2: Add the `applyJobEnrichment` helper**

Immediately after the `toJobResponse` function (after line ~105 in `internal/api/jobs.go`), add:

```go
// applyJobEnrichment sets the list-only fields (task progress, timing, schedule
// source) on a jobResponse. totalTasks/doneTasks come from the LATERAL aggregate;
// startedAt/finishedAt/scheduledJobName are nullable; scheduledJobID comes from
// the job row directly.
func applyJobEnrichment(resp *jobResponse, totalTasks, doneTasks int64, startedAt, finishedAt pgtype.Timestamptz, scheduledJobID pgtype.UUID, scheduledJobName *string) {
	resp.TotalTasks = int32(totalTasks)
	resp.DoneTasks = int32(doneTasks)
	if startedAt.Valid {
		t := startedAt.Time
		resp.StartedAt = &t
	}
	if finishedAt.Valid {
		t := finishedAt.Time
		resp.FinishedAt = &t
	}
	if scheduledJobID.Valid {
		resp.ScheduledJobID = uuidStr(scheduledJobID)
	}
	if scheduledJobName != nil {
		resp.ScheduledJobName = *scheduledJobName
	}
}
```

- [ ] **Step 3: Wire the helper into all 12 `jobRowToResponse*` functions**

Each `jobRowToResponse*` function currently ends with `return toJobResponse(job, r.SubmittedByEmail, nil, nil)`. For every one of the 12, change that tail to capture the response, enrich it, and return it. The added call is identical in every function because the generated row field names match across all row types:

```go
	resp := toJobResponse(job, r.SubmittedByEmail, nil, nil)
	applyJobEnrichment(&resp, r.TotalTasks, r.DoneTasks, r.StartedAt, r.FinishedAt, r.ScheduledJobID, r.ScheduledJobName)
	return resp
```

Apply to: `jobRowToResponseDefault`, `jobRowToResponseByStatus`, `jobRowToResponseByScheduled`, `jobRowToResponseByCreatedAsc`, `jobRowToResponseByNameDesc`, `jobRowToResponseByNameAsc`, `jobRowToResponseByPriorityDesc`, `jobRowToResponseByPriorityAsc`, `jobRowToResponseByStatusDesc`, `jobRowToResponseByStatusAsc`, `jobRowToResponseByUpdatedDesc`, `jobRowToResponseByUpdatedAsc`. (`r.ScheduledJobID` already exists on every row because it is part of `j.*`.) Leave the `jobsRowKey*` functions unchanged.

- [ ] **Step 4: Verify the build**

Run: `go build ./...`
Expected: PASS.

- [ ] **Step 5: Run the api unit tests to confirm no regression**

Run: `go test ./internal/api/... -timeout 60s`
Expected: PASS. (This runs the non-integration unit tests such as `job_spec_test.go`. The list sort/pagination/enrichment integration tests are tagged `//go:build integration` and run in Task 4 and Final verification.)

- [ ] **Step 6: Commit**

```bash
git add internal/api/jobs.go
git commit -m "feat(api): surface task progress, timing, and schedule source on jobs list rows"
```

---

### Task 3: Jobs stats endpoint

**Files:**
- Modify: `internal/api/jobs.go`, `internal/api/server.go`

- [ ] **Step 1: Add the response struct and handler**

In `internal/api/jobs.go`, after `applyJobEnrichment` (from Task 2), add:

```go
// jobStatsResponse is the fleet-wide KPI summary returned by GET /v1/jobs/stats.
// done_24h and failed_24h are windowed on updated_at (see JobStatusCounts).
type jobStatsResponse struct {
	Running   int64 `json:"running"`
	Queued    int64 `json:"queued"`
	Done24h   int64 `json:"done_24h"`
	Failed24h int64 `json:"failed_24h"`
}

func (s *Server) handleJobStats(w http.ResponseWriter, r *http.Request) {
	counts, err := s.q.JobStatusCounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "job stats failed")
		return
	}
	writeJSON(w, http.StatusOK, jobStatsResponse{
		Running:   counts.Running,
		Queued:    counts.Queued,
		Done24h:   counts.Done24h,
		Failed24h: counts.Failed24h,
	})
}
```

- [ ] **Step 2: Register the route**

In `internal/api/server.go`, immediately after the `GET /v1/jobs` registration (line ~103), add:

```go
	mux.Handle("GET /v1/jobs/stats", auth(http.HandlerFunc(s.handleJobStats)))
```

Place it before any `GET /v1/jobs/{id}` route so the static `/stats` path is not captured by the `{id}` pattern. (Go 1.22 ServeMux prefers the more specific static segment, but ordering it first is unambiguous.)

- [ ] **Step 3: Verify the build**

Run: `go build ./...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/api/jobs.go internal/api/server.go
git commit -m "feat(api): add GET /v1/jobs/stats KPI aggregate endpoint"
```

---

### Task 4: Backend integration tests

**Files:**
- Create: `internal/api/jobs_enrichment_integration_test.go`
- Create: `internal/api/jobs_stats_integration_test.go`

- [ ] **Step 1: Write the enrichment integration test**

Create `internal/api/jobs_enrichment_integration_test.go`:

```go
//go:build integration

package api_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

// Submits a job, then drives two of its three tasks to done with timing, and
// asserts the list row reports total/done counts, started_at, and (when the job
// is schedule-spawned) the schedule name.
func TestListJobs_Enrichment(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Enrich", "enrich-user@test.com", false)
	token := createTestToken(t, q, user.ID)

	// A scheduled job owned by the user, and a job linked to it.
	var schedID pgtype.UUID
	err := pool.QueryRow(t.Context(),
		`INSERT INTO scheduled_jobs (name, owner_id, cron_expr, job_spec, next_run_at)
		 VALUES ('nightly-etl', $1, '@daily', '{}'::jsonb, NOW()) RETURNING id`,
		user.ID).Scan(&schedID)
	require.NoError(t, err)

	var jobID pgtype.UUID
	err = pool.QueryRow(t.Context(),
		`INSERT INTO jobs (name, priority, submitted_by, scheduled_job_id)
		 VALUES ('etl-run', 'normal', $1, $2) RETURNING id`,
		user.ID, schedID).Scan(&jobID)
	require.NoError(t, err)

	// Three tasks: two done (with started/finished), one pending. The `commands`
	// column is JSONB NOT NULL DEFAULT '[]' so it is omitted here.
	started := time.Now().Add(-10 * time.Minute)
	finished := time.Now().Add(-2 * time.Minute)
	for i, st := range []string{"done", "done", "pending"} {
		var sAt, fAt any
		if st == "done" {
			sAt, fAt = started, finished
		}
		_, err = pool.Exec(t.Context(),
			`INSERT INTO tasks (job_id, name, status, started_at, finished_at)
			 VALUES ($1, $2, $3, $4, $5)`,
			jobID, fmt.Sprintf("t%d", i), st, sAt, fAt)
		require.NoError(t, err)
	}

	code, page := getJobsPage(t, srv, token, "")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, page.Items, 1)
	row := page.Items[0]

	require.EqualValues(t, 3, row["total_tasks"])
	require.EqualValues(t, 2, row["done_tasks"])
	require.Equal(t, "nightly-etl", row["scheduled_job_name"])
	require.NotEmpty(t, row["started_at"])
	require.NotEmpty(t, row["finished_at"])
}
```

- [ ] **Step 2: Run the enrichment test to verify it passes**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestListJobs_Enrichment -v -timeout 120s`
Expected: PASS. (Requires Docker Desktop running.)

- [ ] **Step 3: Write the stats integration test**

Create `internal/api/jobs_stats_integration_test.go`:

```go
//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

func getJobStats(t *testing.T, srv interface {
	Handler() http.Handler
}, token string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("GET", "/v1/jobs/stats", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var body map[string]any
	if rec.Code == http.StatusOK {
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	}
	return rec.Code, body
}

func TestJobStats_BucketsAndWindow(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Stats", "job-stats-user@test.com", false)
	token := createTestToken(t, q, user.ID)

	seed := func(status string, updatedAgo string) {
		var id pgtype.UUID
		err := pool.QueryRow(t.Context(),
			`INSERT INTO jobs (name, priority, submitted_by, status)
			 VALUES ('j', 'normal', $1, $2) RETURNING id`, user.ID, status).Scan(&id)
		require.NoError(t, err)
		_, err = pool.Exec(t.Context(),
			`UPDATE jobs SET updated_at = NOW() - $2::interval WHERE id = $1`, id, updatedAgo)
		require.NoError(t, err)
	}

	// running=2 (running + dispatched), queued=2 (queued + pending),
	// done_24h=1 (a second done is 48h old, outside the window),
	// failed_24h=2 (failed + timed_out within 24h).
	seed("running", "1 hour")
	seed("dispatched", "1 hour")
	seed("queued", "1 hour")
	seed("pending", "1 hour")
	seed("done", "1 hour")
	seed("done", "48 hours") // outside window - not counted
	seed("failed", "1 hour")
	seed("timed_out", "1 hour")
	seed("cancelled", "1 hour") // in no bucket

	code, body := getJobStats(t, srv, token)
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 2, body["running"])
	require.EqualValues(t, 2, body["queued"])
	require.EqualValues(t, 1, body["done_24h"])
	require.EqualValues(t, 2, body["failed_24h"])
}
```

- [ ] **Step 4: Run the stats test to verify it passes**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestJobStats_BucketsAndWindow -v -timeout 120s`
Expected: PASS.

- [ ] **Step 5: Run the full jobs integration suite to confirm enrichment didn't break sort/pagination**

Run: `go test -tags integration -p 1 ./internal/api/... -run 'TestListJobs|TestJobStats' -v -timeout 180s`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/jobs_enrichment_integration_test.go internal/api/jobs_stats_integration_test.go
git commit -m "test(api): integration coverage for jobs list enrichment and stats buckets"
```

---

## Frontend

All frontend commands run from `web/`. Run a single test file with: `npm test -- src/jobs/<file>` (Vitest). Run the whole suite with `npm test`.

### Task 5: Jobs API client

**Files:**
- Create: `web/src/jobs/api.ts`
- Test: `web/src/jobs/api.test.ts`

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/api.test.ts`:

```ts
import { http, HttpResponse } from 'msw'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { ApiError } from '../lib/api'
import { listJobs, getJobStats, type JobsPage } from './api'

const emptyPage: JobsPage = { items: [], next_cursor: '', total: 0 }

test('unfiltered list sends sort + limit, no status', async () => {
  let captured: URLSearchParams | undefined
  server.use(
    http.get('/v1/jobs', ({ request }) => {
      captured = new URL(request.url).searchParams
      return HttpResponse.json(emptyPage)
    }),
  )
  await listJobs('name')
  expect(captured?.get('sort')).toBe('name')
  expect(captured?.get('limit')).toBe('50')
  expect(captured?.get('status')).toBeNull()
})

test('status filter omits sort (server 400s sort+status)', async () => {
  let captured: URLSearchParams | undefined
  server.use(
    http.get('/v1/jobs', ({ request }) => {
      captured = new URL(request.url).searchParams
      return HttpResponse.json(emptyPage)
    }),
  )
  await listJobs('name', 'running')
  expect(captured?.get('status')).toBe('running')
  expect(captured?.get('sort')).toBeNull()
})

test('passes cursor when provided', async () => {
  let captured: URLSearchParams | undefined
  server.use(
    http.get('/v1/jobs', ({ request }) => {
      captured = new URL(request.url).searchParams
      return HttpResponse.json(emptyPage)
    }),
  )
  await listJobs('-created_at', '', 'CUR')
  expect(captured?.get('cursor')).toBe('CUR')
})

test('getJobStats fetches /jobs/stats', async () => {
  server.use(
    http.get('/v1/jobs/stats', () =>
      HttpResponse.json({ running: 3, queued: 1, done_24h: 487, failed_24h: 12 }),
    ),
  )
  const stats = await getJobStats()
  expect(stats.running).toBe(3)
  expect(stats.done_24h).toBe(487)
})

test('throws ApiError on the error envelope', async () => {
  server.use(http.get('/v1/jobs', () => HttpResponse.json({ error: 'boom' }, { status: 500 })))
  await expect(listJobs('-created_at')).rejects.toBeInstanceOf(ApiError)
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npm test -- src/jobs/api`
Expected: FAIL ("Cannot find module './api'").

- [ ] **Step 3: Write the implementation**

Create `web/src/jobs/api.ts`:

```ts
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
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npm test -- src/jobs/api`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/api.ts web/src/jobs/api.test.ts
git commit -m "feat(web): jobs API client (list + stats)"
```

---

### Task 6: Status and formatting helpers

**Files:**
- Create: `web/src/jobs/status.ts`
- Test: `web/src/jobs/status.test.ts`

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/status.test.ts`:

```ts
import { expect, test } from 'vitest'
import { statusColor, progressPct, formatDuration, formatStarted } from './status'

test('statusColor maps each bucket', () => {
  expect(statusColor('done').dot).toBe('bg-ok')
  expect(statusColor('running').dot).toBe('bg-accent')
  expect(statusColor('dispatched').dot).toBe('bg-accent')
  expect(statusColor('queued').dot).toBe('bg-warn')
  expect(statusColor('pending').dot).toBe('bg-warn')
  expect(statusColor('failed').dot).toBe('bg-err')
  expect(statusColor('timed_out').dot).toBe('bg-err')
  expect(statusColor('cancelled').dot).toBe('bg-fg-mute')
})

test('progressPct rounds done/total, 0 when no tasks', () => {
  expect(progressPct(48, 64)).toBe(75)
  expect(progressPct(0, 0)).toBe(0)
  expect(progressPct(undefined, undefined)).toBe(0)
})

test('formatDuration uses finished when present, now otherwise', () => {
  const started = '2026-06-05T12:00:00Z'
  const finished = '2026-06-05T12:14:00Z'
  expect(formatDuration(started, finished)).toBe('14m')
  const now = new Date('2026-06-05T14:14:00Z').getTime()
  expect(formatDuration(started, undefined, now)).toBe('2h 14m')
})

test('formatDuration returns dash when not started', () => {
  expect(formatDuration(undefined, undefined)).toBe('-')
})

test('formatStarted returns dash when null', () => {
  expect(formatStarted(undefined)).toBe('-')
  expect(formatStarted('2026-06-05T12:00:00Z')).not.toBe('-')
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npm test -- src/jobs/status`
Expected: FAIL ("Cannot find module './status'").

- [ ] **Step 3: Write the implementation**

Create `web/src/jobs/status.ts`:

```ts
import type { JobStatus } from './api'

interface StatusView {
  text: string
  dot: string
}

// Color mapping follows the hi-fi HoloJobsList (the picked direction):
// done=ok, running/dispatched=accent, queued/pending=warn, failed/timed_out=err,
// everything else (cancelled, unknown) = fg-mute.
export function statusColor(status: JobStatus): StatusView {
  switch (status) {
    case 'done':
      return { text: 'text-ok', dot: 'bg-ok' }
    case 'running':
    case 'dispatched':
      return { text: 'text-accent', dot: 'bg-accent' }
    case 'queued':
    case 'pending':
      return { text: 'text-warn', dot: 'bg-warn' }
    case 'failed':
    case 'timed_out':
      return { text: 'text-err', dot: 'bg-err' }
    default:
      return { text: 'text-fg-mute', dot: 'bg-fg-mute' }
  }
}

export function progressPct(done?: number, total?: number): number {
  if (!total || total <= 0) return 0
  return Math.round(((done ?? 0) / total) * 100)
}

// Compact duration between started and finished (or now if still running).
// Returns "-" when the job has not started.
export function formatDuration(startedAt?: string, finishedAt?: string, now = Date.now()): string {
  if (!startedAt) return '-'
  const start = new Date(startedAt).getTime()
  const end = finishedAt ? new Date(finishedAt).getTime() : now
  let secs = Math.max(0, Math.round((end - start) / 1000))
  const h = Math.floor(secs / 3600)
  secs -= h * 3600
  const m = Math.floor(secs / 60)
  secs -= m * 60
  if (h > 0) return `${h}h ${m}m`
  if (m > 0) return `${m}m`
  return `${secs}s`
}

// Short absolute start time, e.g. "Jun 5 · 12:00". Returns "-" when null.
export function formatStarted(startedAt?: string): string {
  if (!startedAt) return '-'
  const d = new Date(startedAt)
  const date = d.toLocaleDateString('en-US', { month: 'short', day: 'numeric' })
  const time = d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false })
  return `${date} · ${time}`
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npm test -- src/jobs/status`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/status.ts web/src/jobs/status.test.ts
git commit -m "feat(web): job status colors and duration/progress formatting helpers"
```

---

### Task 7: Polling hooks

**Files:**
- Create: `web/src/jobs/useJobs.ts`, `web/src/jobs/useJobStats.ts`
- Test: `web/src/jobs/useJobs.test.tsx`, `web/src/jobs/useJobStats.test.tsx`

- [ ] **Step 1: Write the failing tests**

Create `web/src/jobs/useJobs.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { useJobs } from './useJobs'

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

test('fetches jobs and refetches on the interval', async () => {
  let count = 0
  server.use(
    http.get('/v1/jobs', () => {
      count++
      return HttpResponse.json({ items: [], next_cursor: '', total: 0 })
    }),
  )
  renderHook(() => useJobs('-created_at', '', '', 20), { wrapper })
  await waitFor(() => expect(count).toBeGreaterThanOrEqual(1))
  await waitFor(() => expect(count).toBeGreaterThanOrEqual(2))
})
```

Create `web/src/jobs/useJobStats.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { useJobStats } from './useJobStats'

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

test('fetches stats', async () => {
  server.use(
    http.get('/v1/jobs/stats', () =>
      HttpResponse.json({ running: 3, queued: 1, done_24h: 9, failed_24h: 2 }),
    ),
  )
  const { result } = renderHook(() => useJobStats(20), { wrapper })
  await waitFor(() => expect(result.current.data?.running).toBe(3))
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npm test -- src/jobs/useJobs src/jobs/useJobStats`
Expected: FAIL ("Cannot find module").

- [ ] **Step 3: Write the implementations**

Create `web/src/jobs/useJobs.ts`:

```ts
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listJobs, type JobSort } from './api'

// Polls one page of jobs. keepPreviousData keeps rows visible while a new
// sort/filter/page loads and between polls, so the table never flashes empty.
// intervalMs defaults to 3000; tests inject a small value.
export function useJobs(sort: JobSort, status: string, cursor: string, intervalMs = 3000) {
  return useQuery({
    queryKey: ['jobs', sort, status, cursor],
    queryFn: () => listJobs(sort, status, cursor),
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
```

Create `web/src/jobs/useJobStats.ts`:

```ts
import { useQuery } from '@tanstack/react-query'
import { getJobStats } from './api'

export function useJobStats(intervalMs = 3000) {
  return useQuery({
    queryKey: ['jobs', 'stats'],
    queryFn: getJobStats,
    refetchInterval: intervalMs,
  })
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npm test -- src/jobs/useJobs src/jobs/useJobStats`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/useJobs.ts web/src/jobs/useJobs.test.tsx web/src/jobs/useJobStats.ts web/src/jobs/useJobStats.test.tsx
git commit -m "feat(web): jobs list and stats polling hooks"
```

---

### Task 8: JobsTable component

**Files:**
- Create: `web/src/jobs/JobsTable.tsx`
- Test: `web/src/jobs/JobsTable.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/JobsTable.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'
import { JobsTable } from './JobsTable'
import type { Job } from './api'

const jobs: Job[] = [
  {
    id: '9F4E1C', name: 'film-x / shot-042 render', priority: 'high', status: 'running',
    submitted_by_email: 'mira@studio.dev', labels: null,
    created_at: '2026-06-05T14:22:00Z', updated_at: '2026-06-05T14:30:00Z',
    total_tasks: 64, done_tasks: 48, started_at: '2026-06-05T14:22:00Z',
    scheduled_job_name: 'nightly-etl',
  },
  {
    id: 'C41A02', name: 'ci build', priority: 'low', status: 'done',
    submitted_by_email: 'ci@studio.dev', labels: null,
    created_at: '2026-06-05T14:30:00Z', updated_at: '2026-06-05T14:34:00Z',
    total_tasks: 12, done_tasks: 12,
  },
]

test('renders job rows with name, owner, and progress percent', () => {
  render(<JobsTable jobs={jobs} />)
  expect(screen.getByText('film-x / shot-042 render')).toBeInTheDocument()
  expect(screen.getByText('mira@studio.dev')).toBeInTheDocument()
  expect(screen.getByText('75%')).toBeInTheDocument()
  expect(screen.getByText('100%')).toBeInTheDocument()
})

test('renders the schedule chip only when scheduled_job_name is present', () => {
  render(<JobsTable jobs={jobs} />)
  expect(screen.getByText(/nightly-etl/)).toBeInTheDocument()
})

test('renders the empty state when there are no jobs', () => {
  render(<JobsTable jobs={[]} />)
  expect(screen.getByText(/no jobs/i)).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npm test -- src/jobs/JobsTable`
Expected: FAIL ("Cannot find module './JobsTable'").

- [ ] **Step 3: Write the implementation**

Create `web/src/jobs/JobsTable.tsx`:

```tsx
import type { Job } from './api'
import { statusColor, progressPct, formatDuration, formatStarted } from './status'

const COLS = 'grid grid-cols-[90px_1fr_120px_150px_120px_70px_150px]'

export function JobsTable({ jobs }: { jobs: Job[] }) {
  if (jobs.length === 0) {
    return (
      <div className="mx-auto mt-10 max-w-md rounded-card border border-border bg-white/5 p-6 text-center text-[13px] text-fg-mute">
        No jobs yet.
      </div>
    )
  }
  return (
    <div className="rounded-card border border-border bg-white/5 backdrop-blur">
      <div className={`${COLS} border-b border-border px-4 py-3 font-mono text-[10px] tracking-wider text-fg-mute`}>
        <span>ID</span>
        <span>NAME</span>
        <span>STATUS</span>
        <span>PROGRESS</span>
        <span>STARTED</span>
        <span>DUR</span>
        <span>OWNER</span>
      </div>
      {jobs.map((j) => {
        const c = statusColor(j.status)
        const pct = progressPct(j.done_tasks, j.total_tasks)
        return (
          <div
            key={j.id}
            className={`${COLS} items-center border-b border-border/40 px-4 py-2 font-mono text-[11.5px]`}
          >
            <span className="text-fg-mute">{j.id.slice(0, 6)}</span>
            <span className="flex min-w-0 items-center gap-2">
              <span className="truncate font-sans text-[13px] text-fg">{j.name}</span>
              {j.scheduled_job_name && (
                <span className="flex-none rounded-full border border-accent-b/40 bg-accent-b/10 px-1.5 py-0.5 text-[9.5px] text-accent-b">
                  ⟳ {j.scheduled_job_name}
                </span>
              )}
            </span>
            <span className={`flex items-center gap-2 ${c.text}`}>
              <span className={`h-1.5 w-1.5 rounded-full ${c.dot}`} />
              {j.status}
            </span>
            <span className="grid grid-cols-[1fr_36px] items-center gap-2 pr-4">
              <span className="relative h-1 overflow-hidden rounded bg-white/10">
                <span
                  className={`absolute inset-y-0 left-0 rounded ${
                    j.status === 'done' ? 'bg-ok' : j.status === 'failed' || j.status === 'timed_out' ? 'bg-err' : 'bg-accent'
                  }`}
                  style={{ width: `${pct}%` }}
                />
              </span>
              <span className="text-right text-fg">{pct}%</span>
            </span>
            <span className="text-fg-mute">{formatStarted(j.started_at)}</span>
            <span className="text-fg-mute">{formatDuration(j.started_at, j.finished_at)}</span>
            <span className="truncate text-[11px] text-fg-mute">{j.submitted_by_email ?? '-'}</span>
          </div>
        )
      })}
    </div>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npm test -- src/jobs/JobsTable`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/JobsTable.tsx web/src/jobs/JobsTable.test.tsx
git commit -m "feat(web): jobs table (presentational rows)"
```

---

### Task 9: SortControl dropdown

**Files:**
- Create: `web/src/jobs/SortControl.tsx`

- [ ] **Step 1: Write the implementation**

There is no existing dropdown primitive in `web/`, so build a minimal native `<select>` styled to the theme. Create `web/src/jobs/SortControl.tsx`:

```tsx
import type { JobSort } from './api'

const OPTIONS: { value: JobSort; label: string }[] = [
  { value: '-created_at', label: 'Newest' },
  { value: 'created_at', label: 'Oldest' },
  { value: 'name', label: 'Name A→Z' },
  { value: '-name', label: 'Name Z→A' },
  { value: '-priority', label: 'Priority high→low' },
  { value: 'priority', label: 'Priority low→high' },
  { value: 'status', label: 'Status A→Z' },
  { value: '-status', label: 'Status Z→A' },
  { value: '-updated_at', label: 'Recently updated' },
  { value: 'updated_at', label: 'Least recently updated' },
]

export function SortControl({
  value,
  onChange,
  disabled,
  disabledHint,
}: {
  value: JobSort
  onChange: (sort: JobSort) => void
  disabled?: boolean
  disabledHint?: string
}) {
  return (
    <select
      aria-label="Sort jobs"
      title={disabled ? disabledHint : undefined}
      value={value}
      disabled={disabled}
      onChange={(e) => onChange(e.target.value as JobSort)}
      className="rounded-full border border-border bg-black/25 px-3 py-1.5 font-sans text-[12px] text-fg outline-none disabled:opacity-50"
    >
      {OPTIONS.map((o) => (
        <option key={o.value} value={o.value} className="bg-bg text-fg">
          {o.label}
        </option>
      ))}
    </select>
  )
}
```

- [ ] **Step 2: Verify it type-checks via the build**

Run: `npm run build`
Expected: PASS (tsc + vite build with no type errors). If `npm run build` is slow, `npx tsc --noEmit` is sufficient.

- [ ] **Step 3: Commit**

```bash
git add web/src/jobs/SortControl.tsx
git commit -m "feat(web): job sort dropdown control"
```

---

### Task 10: JobsPage composition

**Files:**
- Create: `web/src/jobs/JobsPage.tsx`
- Test: `web/src/jobs/JobsPage.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/jobs/JobsPage.test.tsx`:

```tsx
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { renderWithQuery } from '../test/renderWithQuery'
import { JobsPage } from './JobsPage'

const page = {
  items: [
    {
      id: '9F4E1C', name: 'film-x render', priority: 'high', status: 'running',
      submitted_by_email: 'mira@studio.dev', labels: null,
      created_at: '2026-06-05T14:22:00Z', updated_at: '2026-06-05T14:30:00Z',
      total_tasks: 64, done_tasks: 48, started_at: '2026-06-05T14:22:00Z',
    },
  ],
  next_cursor: '',
  total: 1,
}

const stats = { running: 3, queued: 1, done_24h: 487, failed_24h: 12 }

beforeEach(() => {
  server.use(http.get('/v1/jobs/stats', () => HttpResponse.json(stats)))
})

test('renders jobs and the KPI strip', async () => {
  server.use(http.get('/v1/jobs', () => HttpResponse.json(page)))
  renderWithQuery(<JobsPage />)
  expect(await screen.findByText('film-x render')).toBeInTheDocument()
  // KPI numbers come from the separately-polled stats query; await them.
  expect(await screen.findByText('487')).toBeInTheDocument() // done_24h
  expect(await screen.findByText('12')).toBeInTheDocument() // failed_24h
})

test('selecting a status chip re-requests with status and disables sort', async () => {
  const requests: URLSearchParams[] = []
  server.use(
    http.get('/v1/jobs', ({ request }) => {
      requests.push(new URL(request.url).searchParams)
      return HttpResponse.json(page)
    }),
  )
  renderWithQuery(<JobsPage />)
  await screen.findByText('film-x render')
  await userEvent.click(screen.getByRole('button', { name: 'Running' }))
  await waitFor(() => expect(requests.some((q) => q.get('status') === 'running')).toBe(true))
  // The status-filtered request must NOT carry a sort param.
  const filtered = requests.find((q) => q.get('status') === 'running')
  expect(filtered?.get('sort')).toBeNull()
  expect(screen.getByLabelText('Sort jobs')).toBeDisabled()
})

test('shows the error banner with retry, then recovers', async () => {
  server.use(http.get('/v1/jobs', () => HttpResponse.json({ error: 'boom' }, { status: 500 })))
  renderWithQuery(<JobsPage />)
  expect(await screen.findByRole('button', { name: /retry/i })).toBeInTheDocument()
  server.use(http.get('/v1/jobs', () => HttpResponse.json(page)))
  await userEvent.click(screen.getByRole('button', { name: /retry/i }))
  expect(await screen.findByText('film-x render')).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npm test -- src/jobs/JobsPage`
Expected: FAIL ("Cannot find module './JobsPage'").

- [ ] **Step 3: Write the implementation**

Create `web/src/jobs/JobsPage.tsx`:

```tsx
import { useState } from 'react'
import { Button } from '../components/Button'
import { useJobs } from './useJobs'
import { useJobStats } from './useJobStats'
import { JobsTable } from './JobsTable'
import { SortControl } from './SortControl'
import type { JobSort } from './api'

const FILTERS: { key: string; label: string; status: string }[] = [
  { key: 'all', label: 'All', status: '' },
  { key: 'running', label: 'Running', status: 'running' },
  { key: 'queued', label: 'Queued', status: 'queued' },
  { key: 'done', label: 'Done', status: 'done' },
  { key: 'failed', label: 'Failed', status: 'failed' },
]

const DEFAULT_SORT: JobSort = '-created_at'

export function JobsPage() {
  const [sort, setSort] = useState<JobSort>(DEFAULT_SORT)
  const [filter, setFilter] = useState('all')
  // Cursor of the current page (''=first). The stack holds the cursors of the
  // pages we paged forward from, so prev can pop back (server returns only
  // next_cursor).
  const [cursor, setCursor] = useState('')
  const [stack, setStack] = useState<string[]>([])

  const status = FILTERS.find((f) => f.key === filter)?.status ?? ''
  const statusFiltered = filter !== 'all'
  const { data, error, isLoading, isFetching, refetch } = useJobs(sort, status, cursor)
  const { data: stats } = useJobStats()

  function pickFilter(key: string) {
    setFilter(key)
    setCursor('')
    setStack([])
    if (key !== 'all') setSort(DEFAULT_SORT) // server rejects sort + status
  }

  function pickSort(s: JobSort) {
    setSort(s)
    setCursor('')
    setStack([])
  }

  function next() {
    if (!data?.next_cursor) return
    setStack((s) => [...s, cursor])
    setCursor(data.next_cursor)
  }

  function prev() {
    setStack((s) => {
      if (s.length === 0) return s
      const copy = [...s]
      const back = copy.pop() ?? ''
      setCursor(back)
      return copy
    })
  }

  if (isLoading && !data) {
    return (
      <div className="flex flex-col gap-2">
        {Array.from({ length: 8 }).map((_, i) => (
          <div key={i} className="h-9 rounded border border-border bg-white/5" />
        ))}
      </div>
    )
  }

  if (error && !data) {
    return (
      <div className="mx-auto mt-10 max-w-md rounded-card border border-border bg-white/5 p-6 text-center">
        <div className="mb-3 text-[13px] text-err">{(error as Error).message}</div>
        <Button className="w-auto px-4" onClick={() => refetch()}>
          Retry
        </Button>
      </div>
    )
  }

  const jobs = data?.items ?? []

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end gap-6">
        <div>
          <div className="font-mono text-[11px] tracking-widest text-fg-mute">OVERVIEW</div>
          <h1 className="text-[32px] font-normal tracking-tight">Jobs</h1>
        </div>
        <div className="flex gap-4 font-mono text-[11px] text-fg-mute">
          <span><b className="text-[18px] text-accent">{stats?.running ?? 0}</b> RUNNING</span>
          <span><b className="text-[18px] text-warn">{stats?.queued ?? 0}</b> QUEUED</span>
          <span><b className="text-[18px] text-ok">{stats?.done_24h ?? 0}</b> DONE·24H</span>
          <span><b className="text-[18px] text-err">{stats?.failed_24h ?? 0}</b> FAILED·24H</span>
        </div>
        <div className="ml-auto flex items-center gap-3">
          <span className="font-mono text-[10px] text-fg-mute">
            <span className={isFetching ? 'text-ok' : 'text-fg-dim'}>●</span> live · auto-refreshing
          </span>
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        {FILTERS.map((f) => (
          <button
            key={f.key}
            type="button"
            aria-pressed={filter === f.key}
            onClick={() => pickFilter(f.key)}
            className={`rounded-full border px-3.5 py-1.5 text-[12px] ${
              filter === f.key ? 'border-accent/60 bg-accent/15 text-fg' : 'border-border bg-white/5 text-fg-mute'
            }`}
          >
            {f.label}
          </button>
        ))}
        <div className="ml-auto">
          <SortControl
            value={sort}
            onChange={pickSort}
            disabled={statusFiltered}
            disabledHint="Sorting is unavailable while a status filter is active - the server rejects sort + status together. Switch to All to sort."
          />
        </div>
      </div>

      <JobsTable jobs={jobs} />

      <div className="flex items-center justify-between px-1 font-mono text-[10.5px] tracking-wider text-fg-mute">
        <span>
          SHOWING <span className="text-fg">1–{jobs.length}</span> OF <span className="text-fg">{data?.total ?? 0}</span>
          {' · '}SORT <span className="text-accent-b">{statusFiltered ? `status=${status}` : sort}</span> · CURSOR PAGINATED
        </span>
        <div className="flex gap-2">
          <button
            type="button"
            onClick={prev}
            disabled={stack.length === 0}
            className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
          >
            ← prev
          </button>
          <button
            type="button"
            onClick={next}
            disabled={!data?.next_cursor}
            className="rounded-full border border-border px-3 py-1 text-[11px] text-fg-mute disabled:opacity-40"
          >
            next 50 →
          </button>
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `npm test -- src/jobs/JobsPage`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/jobs/JobsPage.tsx web/src/jobs/JobsPage.test.tsx
git commit -m "feat(web): jobs page (KPI strip, status filters, sort, pagination)"
```

---

### Task 11: Route wiring

**Files:**
- Modify: `web/src/app/router.tsx`
- Delete: `web/src/app/JobsPlaceholder.tsx`

- [ ] **Step 1: Point `/jobs` at `JobsPage`**

In `web/src/app/router.tsx`, replace the `JobsPlaceholder` import line:

```tsx
import { JobsPlaceholder } from './JobsPlaceholder'
```

with:

```tsx
import { JobsPage } from '../jobs/JobsPage'
```

Wait - `JobsPlaceholder` is still used by the `/schedules`, `/admin`, and `/profile/*` routes, so do NOT remove its import or the file. Instead: KEEP the existing `JobsPlaceholder` import, ADD the `JobsPage` import alongside it, and change ONLY the `/jobs` route element to `<JobsPage />`:

```tsx
        <Route path="/jobs" element={<JobsPage />} />
```

Leave the `/schedules`, `/admin`, and `/profile/*` routes pointing at `<JobsPlaceholder />` as they are.

- [ ] **Step 2: Verify build and full frontend suite**

Run: `npm run build && npm test`
Expected: build PASS; all Vitest suites PASS (including existing workers/auth tests).

- [ ] **Step 3: Commit**

```bash
git add web/src/app/router.tsx
git commit -m "feat(web): route /jobs to the JobsPage"
```

---

## Final verification

- [ ] **Step 1: Backend unit + build**

Run: `make test` then `go build ./...`
Expected: PASS.

- [ ] **Step 2: Backend integration (Docker required)**

Run: `go test -tags integration -p 1 ./internal/api/... -run 'TestListJobs|TestJobStats' -v -timeout 240s`
Expected: PASS.

- [ ] **Step 3: Frontend build + tests**

Run (from `web/`): `npm run build && npm test`
Expected: PASS.

- [ ] **Step 4: Manual smoke (optional but recommended)**

Start the stack, log in, open `/jobs`. Confirm: the KPI strip shows fleet-wide counts; rows show progress bars and durations; the schedule chip appears on schedule-spawned jobs; status chips filter and disable the sort dropdown; sort works when unfiltered; next/prev paginate and prev is disabled on page 1.

---

## Notes for the implementer

- **Never hand-edit** `internal/store/*.sql.go` or `internal/store/models.go` - they are sqlc output. Edit `internal/store/query/jobs.sql` and run `make generate`.
- The enrichment is **list-only**. `GetJobWithEmail` / `handleGetJob` is intentionally unchanged; a future job-detail page derives progress from the full task list it already returns.
- The 12 list queries differ only in `WHERE`/`ORDER BY`; the enrichment block (two joins + three SELECT columns) is identical for all of them, and the `applyJobEnrichment` call is identical in all 12 response mappers because the generated field names match.
- Deferred to backlog (out of scope here): Lanes view, Timeline view, "My jobs" toggle, search box, job-detail page and row-click navigation.
