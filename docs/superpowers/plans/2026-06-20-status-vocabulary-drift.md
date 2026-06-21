# Status Vocabulary Drift Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add CHECK constraints to the six free-TEXT status/vocabulary columns, reconcile the `JobStatusCounts` KPI query to the real `jobs.status` vocabulary, and validate `Priority` in the single shared job-spec validator.

**Architecture:** Three coupled backend changes that must land together: (1) migration `000019` adds six `CHECK` constraints plus a one-row-safe `jobs.priority` normalization; (2) `internal/store/query/jobs.sql` `JobStatusCounts` filters are reconciled and regenerated via sqlc; (3) `jobspec.Validate` gains a `Priority` switch. The stats integration test currently seeds statuses the new `jobs_status_check` rejects, so the test rewrite and the constraint must ship in the same change.

**Tech Stack:** Go, Postgres 16, sqlc, golang-migrate, testcontainers-go, testify.

**Slice independence:** This is a SINGLE SEQUENTIAL BACKEND SLICE. There is no frontend work and no parallelism. The `cancelled` job count folds into the existing `failed_24h` response field, so the four-field `jobStatsResponse` and the dashboard are untouched. Execute tasks strictly in order: the constraint (Task 4) must not land before the stats test that violates it is rewritten (Task 3).

---

## Confirmed grounding (read before starting)

These were verified against the live code, not assumed:

- **`jobspec.Validate` lives at `internal/jobspec/jobspec.go:71`.** `internal/api/job_spec.go:15-24` only re-exports it via type aliases and `ValidateJobSpec`. `internal/jobcreate/jobcreate.go:32` also calls `jobspec.Validate`. This is the single shared ingestion path (REST/CLI/MCP/schedrunner). `JobSpec.Priority` is a `string` field (`jobspec.go:18`) and is currently never validated. `jobcreate.go:36-39` substitutes `"normal"` for empty `Priority`, so empty must stay valid.
- **Next migration number is `000019`.** `000018_hot_path_indexes` is the latest in `internal/store/migrations/`.
- **`overlap_policy` is already validated at the handler** on both create (`internal/api/scheduled_jobs.go:90-95`) and update (`internal/api/scheduled_jobs.go:555-560`), rejecting anything other than `skip`/`allow`. No normalization `UPDATE` is needed for `overlap_policy`. Only `jobs.priority` needs the normalization `UPDATE` (it was never validated).
- **The only constraint-violating test is `internal/api/jobs_stats_integration_test.go`** (seeds `dispatched`/`queued`/`timed_out` directly into `jobs.status`). Every other test that inserts literals seeds in-vocabulary values: `workers_sort_integration_test.go` (online/offline/stale), `workers_revoked_list_integration_test.go` (revoked), `tasks_integration_test.go` (stdout), `jobs_enrichment_integration_test.go` (done/pending task statuses), `scheduled_jobs_sort_integration_test.go` and `schedrunner/runner_test.go` (overlap_policy skip), `store/migrate_down_test.go` (no status/priority literals). None of those need changes.

## The six allowed-value sets (verified against write sites)

- `workers.status` -> `online, offline, stale, revoked`
- `jobs.status` -> `pending, running, done, failed, cancelled`
- `jobs.priority` -> `low, normal, high`
- `tasks.status` -> `pending, dispatched, running, done, failed, timed_out`
- `task_logs.stream` -> `stdout, stderr`
- `scheduled_jobs.overlap_policy` -> `skip, allow`

---

### Task 1: Validate Priority in jobspec.Validate (unit, no DB)

This is first because it has no DB dependency and establishes the application-layer guard whose set must match the migration's `jobs_priority_check`.

**Files:**
- Modify: `internal/jobspec/jobspec.go:71-77` (add a switch after the tasks-length check, before the per-task loop)
- Test: `internal/jobspec/jobspec_test.go` (append new tests)

- [ ] **Step 1: Write the failing tests**

Append to `internal/jobspec/jobspec_test.go`:

```go
func TestValidate_PriorityEmptyOK(t *testing.T) {
	spec := JobSpec{
		Name:  "ok",
		Tasks: []TaskSpec{{Name: "t1", Command: []string{"echo", "hi"}}},
	}
	require.NoError(t, Validate(&spec))
}

func TestValidate_PriorityKnownLevelsOK(t *testing.T) {
	for _, p := range []string{"low", "normal", "high"} {
		spec := JobSpec{
			Name:     "ok",
			Priority: p,
			Tasks:    []TaskSpec{{Name: "t1", Command: []string{"echo", "hi"}}},
		}
		require.NoError(t, Validate(&spec), "priority %q must be accepted", p)
	}
}

func TestValidate_PriorityTypoRejected(t *testing.T) {
	spec := JobSpec{
		Name:     "x",
		Priority: "hgih",
		Tasks:    []TaskSpec{{Name: "t1", Command: []string{"echo", "hi"}}},
	}
	err := Validate(&spec)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid priority")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/jobspec/... -run TestValidate_Priority -v -timeout 30s`
Expected: `TestValidate_PriorityTypoRejected` FAILS (no error returned); the two OK tests pass already.

- [ ] **Step 3: Add the Priority switch**

In `internal/jobspec/jobspec.go`, insert after the `len(spec.Tasks) == 0` check (currently ends at line 77) and before `nameSet := ...`:

```go
	// Priority is optional. Empty is allowed (jobcreate defaults it to "normal").
	// A non-empty value must be one of the known levels; this rejects typos that
	// would otherwise be stored silently and break the jobs_priority_check
	// constraint. This set MUST stay identical to jobs_priority_check in
	// migration 000019_status_vocabulary_checks.
	switch spec.Priority {
	case "", "low", "normal", "high":
		// ok
	default:
		return fmt.Errorf("invalid priority %q: must be low, normal, or high", spec.Priority)
	}
```

(`fmt` is already imported in this file.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/jobspec/... -run TestValidate_Priority -v -timeout 30s`
Expected: PASS (all three).

- [ ] **Step 5: Run the full jobspec package to confirm no regression**

Run: `go test ./internal/jobspec/... -v -timeout 30s`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/jobspec/jobspec.go internal/jobspec/jobspec_test.go
git commit -m "feat(jobspec): validate priority against {low,normal,high}"
```

---

### Task 2: Reconcile JobStatusCounts query and regenerate sqlc

This lands before the migration so the query already matches the real vocabulary when the constraint arrives. The query is application code, not schema, and is regenerated by sqlc.

**Files:**
- Modify: `internal/store/query/jobs.sql:287-292` (the `JobStatusCounts` SELECT)
- Regenerated (do NOT hand-edit): `internal/store/jobs.sql.go`

- [ ] **Step 1: Edit the query SELECT**

Replace the `JobStatusCounts` SELECT body in `internal/store/query/jobs.sql` (lines 287-292) with:

```sql
SELECT
  COUNT(*) FILTER (WHERE status = 'running')                                                              AS running,
  COUNT(*) FILTER (WHERE status = 'pending')                                                              AS queued,
  COUNT(*) FILTER (WHERE status = 'done'                  AND updated_at >= NOW() - INTERVAL '24 hours')  AS done_24h,
  COUNT(*) FILTER (WHERE status IN ('failed','cancelled') AND updated_at >= NOW() - INTERVAL '24 hours')  AS failed_24h
FROM jobs;
```

Leave the `-- name: JobStatusCounts :one` line and the comment block above (lines 282-286) unchanged. The response field names (`running`, `queued`, `done_24h`, `failed_24h`) are deliberately preserved: `queued` now counts `pending`, but renaming the JSON field would break the API contract and is out of scope.

- [ ] **Step 2: Regenerate sqlc bindings**

Run: `make generate`
Expected: `internal/store/jobs.sql.go` updates the `jobStatusCounts` SQL constant to match. sqlc emits LF line endings; on this CRLF repo it rewrites line endings across all generated files.

- [ ] **Step 3: Apply CLAUDE.md CRLF hygiene**

Run: `git diff --ignore-all-space`
Inspect: the ONLY real content hunk should be the four `COUNT(*) FILTER` lines inside the `jobStatusCounts` constant in `internal/store/jobs.sql.go`. Every other changed file (or other hunk in this file) is an LF-only rewrite.

For each generated file whose diff is LF-only (no real content change), revert it:

```bash
git checkout -- <file-with-only-LF-changes>
```

Re-run `git diff --ignore-all-space` until the only remaining hunk is the `JobStatusCounts` filter change.

- [ ] **Step 4: Build to confirm the generated code compiles**

Run: `go build ./...`
Expected: success.

- [ ] **Step 5: Commit**

```bash
git add internal/store/query/jobs.sql internal/store/jobs.sql.go
git commit -m "fix(store): reconcile JobStatusCounts to real jobs.status vocabulary"
```

---

### Task 3: Rewrite the stats integration test for the new buckets

This MUST land before the constraint (Task 4). It currently seeds `dispatched`/`queued`/`timed_out` into `jobs.status`, which `jobs_status_check` will reject; rewriting it to seed only valid statuses and assert the reconciled buckets is the load-bearing reason the query and constraint ship together.

**Files:**
- Modify: `internal/api/jobs_stats_integration_test.go:46-64`

- [ ] **Step 1: Rewrite the seed block and assertions**

Replace lines 46-64 of `internal/api/jobs_stats_integration_test.go` (the seed calls through the assertions) with:

```go
	// New bucketing after JobStatusCounts reconciliation:
	//   running    = COUNT(status = 'running')
	//   queued     = COUNT(status = 'pending')
	//   done_24h   = COUNT(status = 'done'                  within 24h)
	//   failed_24h = COUNT(status IN ('failed','cancelled') within 24h)
	// Only valid jobs.status values may be seeded now that jobs_status_check
	// exists: pending, running, done, failed, cancelled.
	seed("running", "1 hour")   // running=1
	seed("pending", "1 hour")   // queued=1
	seed("done", "1 hour")      // done_24h=1
	seed("done", "48 hours")    // outside window - not counted
	seed("failed", "1 hour")    // failed_24h += 1
	seed("cancelled", "1 hour") // failed_24h += 1 (cancelled folds into failed_24h)

	code, body := getJobStats(t, srv, token)
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 1, body["running"])
	require.EqualValues(t, 1, body["queued"])
	require.EqualValues(t, 1, body["done_24h"])
	require.EqualValues(t, 2, body["failed_24h"])
```

Leave the `seed` helper (lines 35-44) unchanged; it inserts `(name, priority, submitted_by, status)` with `priority='normal'` and the given status, then ages `updated_at`.

- [ ] **Step 2: Run the test to verify it passes against the reconciled query**

Note: at this point in the sequence the constraint is NOT yet applied, so all seeded values insert fine; this step verifies the query reconciliation from Task 2 produces the expected buckets.

Run: `go test -tags integration -p 1 ./internal/api/... -run TestJobStats_BucketsAndWindow -v -timeout 120s`
Expected: PASS. (Requires Docker Desktop running.)

- [ ] **Step 3: Commit**

```bash
git add internal/api/jobs_stats_integration_test.go
git commit -m "test(api): rewrite job-stats test for reconciled buckets and valid statuses"
```

---

### Task 4: Add migration 000019 (CHECK constraints + priority normalization)

Now that the query is reconciled (Task 2) and the only violating test is fixed (Task 3), the tree is safe for the constraints. Migration files are embedded and run on startup; the integration suite migrates a fresh container, so a bad set here breaks every integration test.

**Files:**
- Create: `internal/store/migrations/000019_status_vocabulary_checks.up.sql`
- Create: `internal/store/migrations/000019_status_vocabulary_checks.down.sql`

- [ ] **Step 1: Write the up migration**

Create `internal/store/migrations/000019_status_vocabulary_checks.up.sql`:

```sql
-- Normalize any historically-drifted priority before constraining (jobspec.Validate
-- never validated priority until 000019's companion change, so a typo could have
-- been persisted). All other constrained columns have only bounded writers and
-- need no cleanup; overlap_policy is validated at the handler.
UPDATE jobs SET priority = 'normal'
WHERE priority NOT IN ('low','normal','high');

ALTER TABLE workers
  ADD CONSTRAINT workers_status_check
  CHECK (status IN ('online','offline','stale','revoked'));

ALTER TABLE jobs
  ADD CONSTRAINT jobs_status_check
  CHECK (status IN ('pending','running','done','failed','cancelled'));

-- This set MUST stay identical to the priority switch in jobspec.Validate.
ALTER TABLE jobs
  ADD CONSTRAINT jobs_priority_check
  CHECK (priority IN ('low','normal','high'));

ALTER TABLE tasks
  ADD CONSTRAINT tasks_status_check
  CHECK (status IN ('pending','dispatched','running','done','failed','timed_out'));

ALTER TABLE task_logs
  ADD CONSTRAINT task_logs_stream_check
  CHECK (stream IN ('stdout','stderr'));

ALTER TABLE scheduled_jobs
  ADD CONSTRAINT scheduled_jobs_overlap_policy_check
  CHECK (overlap_policy IN ('skip','allow'));
```

- [ ] **Step 2: Write the down migration**

Create `internal/store/migrations/000019_status_vocabulary_checks.down.sql`:

```sql
ALTER TABLE scheduled_jobs DROP CONSTRAINT IF EXISTS scheduled_jobs_overlap_policy_check;
ALTER TABLE task_logs      DROP CONSTRAINT IF EXISTS task_logs_stream_check;
ALTER TABLE tasks          DROP CONSTRAINT IF EXISTS tasks_status_check;
ALTER TABLE jobs           DROP CONSTRAINT IF EXISTS jobs_priority_check;
ALTER TABLE jobs           DROP CONSTRAINT IF EXISTS jobs_status_check;
ALTER TABLE workers        DROP CONSTRAINT IF EXISTS workers_status_check;
```

The down drops only the constraints. It does NOT restore normalized priority values (data normalization is not reversible and the old typos carried no meaning) and does NOT restore the old `JobStatusCounts` query (that is application code in Task 2, versioned independently of the schema). `IF EXISTS` keeps the down idempotent, matching the 000018 convention. Plain `ALTER TABLE ... ADD CONSTRAINT` is fine inside golang-migrate's per-migration transaction (unlike `CREATE INDEX CONCURRENTLY`, which 000018 avoided).

- [ ] **Step 3: Run the existing migration test to confirm 000019 applies cleanly**

Run: `go test -tags integration -p 1 ./internal/store/... -run TestMigrate -v -timeout 120s`
Expected: PASS (migrates a fresh container up through 000019 twice; the second is a no-op).

- [ ] **Step 4: Run the full api integration suite to confirm no seeded test violates the new constraints**

Run: `go test -tags integration -p 1 ./internal/api/... -timeout 300s`
Expected: PASS. (This is the regression net: any test seeding an out-of-vocabulary literal would now fail. Task 3 already fixed the only such test.)

- [ ] **Step 5: Commit**

```bash
git add internal/store/migrations/000019_status_vocabulary_checks.up.sql internal/store/migrations/000019_status_vocabulary_checks.down.sql
git commit -m "feat(store): add 000019 status vocabulary CHECK constraints"
```

---

### Task 5: Constraint round-trip and rejection integration tests

Adds the regression guard the bug says was missing: assert each constraint rejects an out-of-set value, the priority normalization runs on up, and the down removes the constraints. Follows the existing `migrate_test.go` / `migrate_down_test.go` / `sort_indexes_integration_test.go` patterns.

**Files:**
- Create: `internal/store/status_vocabulary_constraints_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/store/status_vocabulary_constraints_test.go`:

```go
//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"relay/internal/store"
)

// seedUserAndJob inserts a user and a valid job, returning their ids. Used to
// satisfy FK constraints when probing tasks/task_logs.
func seedUserAndJob(t *testing.T, pool *pgxpool.Pool) (userID, jobID string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		"u-"+t.Name(), t.Name()+"@example.com").Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO jobs (name, submitted_by) VALUES ('j', $1) RETURNING id`,
		userID).Scan(&jobID))
	return userID, jobID
}

// TestStatusVocabularyConstraints_Reject confirms migration 000019's six CHECK
// constraints reject an out-of-vocabulary value on each column.
func TestStatusVocabularyConstraints_Reject(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	userID, jobID := seedUserAndJob(t, pool)

	var taskID string
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO tasks (job_id, name) VALUES ($1, 'tt') RETURNING id`,
		jobID).Scan(&taskID))

	// workers.status
	_, err := pool.Exec(ctx,
		`INSERT INTO workers (name, hostname, cpu_cores, ram_gb, gpu_count, gpu_model, os, status)
		 VALUES ('w', 'w-host', 4, 16, 0, '', 'linux', 'bogus')`)
	require.Error(t, err, "workers_status_check must reject 'bogus'")

	// jobs.status
	_, err = pool.Exec(ctx,
		`INSERT INTO jobs (name, submitted_by, status) VALUES ('j2', $1, 'dispatched')`, userID)
	require.Error(t, err, "jobs_status_check must reject 'dispatched'")

	// jobs.priority
	_, err = pool.Exec(ctx,
		`INSERT INTO jobs (name, submitted_by, priority) VALUES ('j3', $1, 'hgih')`, userID)
	require.Error(t, err, "jobs_priority_check must reject 'hgih'")

	// tasks.status
	_, err = pool.Exec(ctx,
		`INSERT INTO tasks (job_id, name, status) VALUES ($1, 'tt2', 'queued')`, jobID)
	require.Error(t, err, "tasks_status_check must reject 'queued'")

	// task_logs.stream
	_, err = pool.Exec(ctx,
		`INSERT INTO task_logs (task_id, stream, content) VALUES ($1, 'syslog', 'x')`, taskID)
	require.Error(t, err, "task_logs_stream_check must reject 'syslog'")

	// scheduled_jobs.overlap_policy
	_, err = pool.Exec(ctx,
		`INSERT INTO scheduled_jobs (name, owner_id, cron_expr, job_spec, overlap_policy, next_run_at)
		 VALUES ('s', $1, '@daily', '{}'::jsonb, 'maybe', NOW())`, userID)
	require.Error(t, err, "scheduled_jobs_overlap_policy_check must reject 'maybe'")
}

// TestStatusVocabularyConstraints_RoundTrip confirms 000019 up normalizes a
// drifted priority and down removes the constraints. It drives golang-migrate
// down to 000018 then back up to confirm the round-trip is clean.
func TestStatusVocabularyConstraints_RoundTrip(t *testing.T) {
	pool, dsn := newMigratedPoolWithDSN(t)
	ctx := context.Background()

	// After full migration, the constraints exist: a bogus job priority is rejected.
	userID, _ := seedUserAndJob(t, pool)
	_, err := pool.Exec(ctx,
		`INSERT INTO jobs (name, submitted_by, priority) VALUES ('jbad', $1, 'urgent')`, userID)
	require.Error(t, err, "jobs_priority_check should be present after up")

	// Migrate down past 000019 (to 000018 = version 18): constraints dropped.
	require.NoError(t, store.MigrateTo(dsn, 18))
	_, err = pool.Exec(ctx,
		`INSERT INTO jobs (name, submitted_by, priority) VALUES ('jbad2', $1, 'urgent')`, userID)
	require.NoError(t, err, "after down to 000018, jobs_priority_check should be gone")

	// Seed a drifted priority so the up normalization has something to fix.
	_, err = pool.Exec(ctx,
		`UPDATE jobs SET priority = 'sometypo' WHERE name = 'jbad2'`)
	require.NoError(t, err)

	// Migrate back up: the UPDATE in 000019 normalizes the drift to 'normal'
	// before adding the constraint, so the up succeeds.
	require.NoError(t, store.MigrateTo(dsn, 19),
		"000019 up must normalize drifted priority before constraining")

	var got string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT priority FROM jobs WHERE name = 'jbad2'`).Scan(&got))
	require.Equal(t, "normal", got, "drifted priority must be normalized to 'normal'")
}
```

Confirm `newTestPool` is the shared integration pool helper in `package store_test` (used by `sort_indexes_integration_test.go:17`) and `newMigratedPoolWithDSN` is the DSN-returning variant in `migrate_down_test.go:24`. Reuse both; do not duplicate them.

- [ ] **Step 2: Run the tests to verify they pass against 000019**

Run: `go test -tags integration -p 1 ./internal/store/... -run TestStatusVocabularyConstraints -v -timeout 180s`
Expected: PASS (both tests).

- [ ] **Step 3: Run the full store integration package to confirm no regression**

Run: `go test -tags integration -p 1 ./internal/store/... -timeout 300s`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/store/status_vocabulary_constraints_test.go
git commit -m "test(store): assert 000019 constraints reject drift and round-trip cleanly"
```

---

### Task 6: Full-suite verification and backlog closeout

**Files:**
- Move: `docs/backlog/bug-2026-06-10-status-vocabulary-drift.md` -> `docs/backlog/closed/bug-2026-06-10-status-vocabulary-drift.md`

- [ ] **Step 1: Run unit tests**

Run: `make test`
Expected: PASS (covers the jobspec priority unit tests; no Docker needed).

- [ ] **Step 2: Run the full integration suite**

Run: `make test-integration`
Expected: PASS. (Requires Docker Desktop and `p4` on PATH.)

- [ ] **Step 3: Close the backlog item**

Confirm a `docs/backlog/closed/` directory exists (create it if the repo convention uses it; check `git log` for prior closeouts). Then:

```bash
git mv docs/backlog/bug-2026-06-10-status-vocabulary-drift.md docs/backlog/closed/bug-2026-06-10-status-vocabulary-drift.md
```

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "chore(backlog): close status-vocabulary-drift bug"
```

---

## Self-review

- **Spec coverage:** Part 1 migration -> Task 4 (+ round-trip/normalization in Task 5). Part 2 query reconciliation -> Task 2 (with the CRLF step). Part 3 priority validation -> Task 1. Testing section: migration round-trip + constraint rejection -> Task 5; stats test rewrite -> Task 3; priority unit tests -> Task 1. Risk 1 (stats test) -> Task 3 ordered before Task 4. Risk 2 (overlap_policy handler) -> confirmed validated, no migration UPDATE needed (documented in grounding). Risk 3 (priority set coupling) -> comments at both `jobspec.go` and the migration. Risk 4 (no shared constants package) -> not introduced; scattered literals left as-is.
- **Ordering safety:** the constraint (Task 4) lands only after the query reconciliation (Task 2) and the only violating test (Task 3) are committed, so the tree never has a constraint an existing test violates.
- **No placeholders:** every code step shows the real edit. The two vocabulary sets that must agree (`jobspec.Validate` switch and `jobs_priority_check`) are identical `low,normal,high` and cross-referenced in comments.
- **Type consistency:** response field names (`running`, `queued`, `done_24h`, `failed_24h`) preserved across Task 2 (query) and Task 3 (assertions). `MigrateTo(dsn, 18/19)` matches the `func MigrateTo(dsn string, version uint)` signature in `internal/store/export_test.go:15`.
