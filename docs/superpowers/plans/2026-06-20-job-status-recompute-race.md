# Atomic Job Status Recompute Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the read-modify-write `updateJobStatusFromTasks` in the worker handler with a single atomic SQL `UPDATE ... FROM (SELECT ...)` so concurrent task-completion writes can never strand a finished job in `running`.

**Architecture:** Add one sqlc query `RecomputeJobStatus` that recomputes a job's status from its tasks and writes it in one statement (last writer always sees committed task state). The handler helper becomes a thin wrapper that calls the new query and returns the resulting status string so the existing terminal SSE `job` event keeps firing. Delete the orphaned dead copy of the helper in `internal/api/jobs.go`.

**Tech Stack:** Go, sqlc, pgx/v5, Postgres, testcontainers-go integration tests.

**Slice independence:** Backend-only. There is no frontend slice. All work is in `internal/store` and `internal/worker` (plus a deletion in `internal/api`).

---

## Background (read before starting)

The live helper at `internal/worker/handler.go:589-618`:

```go
func updateJobStatusFromTasks(ctx context.Context, q *store.Queries, jobID pgtype.UUID) string {
	tasks, err := q.ListTasksByJob(ctx, jobID)
	if err != nil || len(tasks) == 0 {
		return ""
	}
	// ... counts done/failed/active, picks running/done/failed ...
	_, _ = q.UpdateJobStatus(ctx, store.UpdateJobStatusParams{ID: jobID, Status: newStatus})
	return newStatus
}
```

It is a list-then-write. Two agents finishing the last two tasks concurrently can interleave so the stale `running` write lands last. This is the race.

Two callers, both in `internal/worker/handler.go`:
- Line 444 (retry path): `updateJobStatusFromTasks(ctx, h.q, task.JobID)` - return value discarded.
- Line 476 (terminal path): `jobStatus := updateJobStatusFromTasks(ctx, h.q, updated.JobID)` - return value drives the terminal SSE `job` event at lines 484-490:

```go
if jobStatus == "done" || jobStatus == "failed" {
	h.broker.Publish(events.Event{
		Type:  "job",
		JobID: uuidStr(updated.JobID),
		Data:  []byte(fmt.Sprintf(`{"id":%q,"status":%q}`, uuidStr(updated.JobID), jobStatus)),
	})
}
```

**The refactor MUST keep returning the new status string** so this SSE emission is preserved. That is the whole point of the bug report (terminal SSE event never fires when the job is stuck `running`).

**Invariant / transaction check:** Neither caller runs inside a transaction; both use `h.q` (the pool) directly. This query writes `jobs.status`, not `tasks.status` or `task_logs`, so the epoch-fence invariant does not apply. No new transaction is needed: the entire recompute is now a single atomic statement, which is strictly safer than the old two-statement version. The new query reads `tasks` for the same `job_id` it updates; under Postgres READ COMMITTED each concurrent caller's `UPDATE` re-reads committed task rows at statement start, so the last writer observes the final task state. No explicit locking required. Do not wrap the call in a tx.

**Dead-copy confirmation:** `internal/api/jobs.go:772-800` defines an identical-logic `updateJobStatusFromTasks` (returns nothing). A grep for `updateJobStatusFromTasks` shows callers only at `internal/worker/handler.go:444` and `:476`, which resolve to the worker package's own copy. The `internal/api` copy has zero callers and is safe to delete (Task 3).

**sqlc / CRLF caveat (applies to Task 1):** `make generate` runs sqlc, which emits LF line endings and on this CRLF repo rewrites line endings across all generated files. After generating, run `git diff --ignore-all-space` and keep only the real content change; revert any LF-only hunks with `git checkout -- <file>`. **Never edit `*.sql.go` or `models.go` by hand.**

---

## Task 1: Add the `RecomputeJobStatus` store query

**Files:**
- Modify: `internal/store/query/jobs.sql` (append after the `UpdateJobStatus` block at lines 83-87)
- Generated (do not hand-edit): `internal/store/jobs.sql.go`

This query belongs in `jobs.sql`, not `scheduled_jobs.sql`: it updates the `jobs` table and lives next to the existing `UpdateJobStatus`.

- [ ] **Step 1: Add the query to `internal/store/query/jobs.sql`**

Append this block immediately after the existing `UpdateJobStatus` query (after line 87):

```sql
-- name: RecomputeJobStatus :one
-- Atomically recomputes a job's status from its tasks in a single statement,
-- so concurrent last-task completions can never strand the job in 'running'.
-- Returns the new status. Returns pgx.ErrNoRows if the job has no tasks
-- (the subquery's aggregate is empty), matching the old helper's "" behavior.
UPDATE jobs j
SET status = sub.next, updated_at = NOW()
FROM (
    SELECT CASE
        WHEN COUNT(*) FILTER (WHERE status NOT IN ('done','failed','timed_out')) > 0 THEN 'running'
        WHEN COUNT(*) FILTER (WHERE status = 'done') = COUNT(*) THEN 'done'
        ELSE 'failed'
    END AS next
    FROM tasks
    WHERE job_id = $1
    HAVING COUNT(*) > 0
) sub
WHERE j.id = $1
RETURNING j.status;
```

Note: the `HAVING COUNT(*) > 0` makes the subquery produce zero rows when the job has no tasks, so the `UPDATE`'s `FROM` join yields no rows and the query returns `pgx.ErrNoRows`. This preserves the old helper's "do nothing, return empty" behavior for the no-tasks case (and matches the `:one` contract).

- [ ] **Step 2: Regenerate the store layer**

Run: `make generate`
Expected: `internal/store/jobs.sql.go` gains a `RecomputeJobStatus` method with signature `func (q *Queries) RecomputeJobStatus(ctx context.Context, jobID pgtype.UUID) (string, error)`.

- [ ] **Step 3: Clean up sqlc's CRLF rewrites**

Run: `git diff --ignore-all-space`
Expected: the only substantive change is the new `RecomputeJobStatus` method in `internal/store/jobs.sql.go` plus the `.sql` edit. For any generated file showing only line-ending churn (no `--ignore-all-space` content diff), revert it:

Run (per spurious file): `git checkout -- <file>`

- [ ] **Step 4: Verify it compiles**

Run: `go build ./internal/store/...`
Expected: builds clean, no errors.

- [ ] **Step 5: Commit**

```bash
git add internal/store/query/jobs.sql internal/store/jobs.sql.go
git commit -m "store: add atomic RecomputeJobStatus query"
```

---

## Task 2: Store-layer integration test for `RecomputeJobStatus`

**Files:**
- Test: `internal/store/store_test.go` (append a new test function; build-tagged `//go:build integration`, package `store_test`)

This must be an integration test (needs real Postgres) - it exercises the `FILTER`/`HAVING` SQL and the concurrent-completion ordering. The file already has `//go:build integration` and the `newTestQueries`, `makeTestUser` helpers.

Helper facts for writing the test:
- `q.CreateJob(...)` creates a job with status `pending`.
- `q.CreateTask(ctx, store.CreateTaskParams{JobID, Name, Commands, Env, Requires})` creates a task at default status `pending`, epoch 0.
- `q.UpdateTaskStatusEpoch(ctx, store.UpdateTaskStatusEpochParams{ID, Status, Epoch: 0})` sets a fresh task's status (epoch 0 matches a freshly created task).
- `q.RecomputeJobStatus(ctx, job.ID)` returns `(string, error)`.

- [ ] **Step 1: Write the failing test**

Append to `internal/store/store_test.go`:

```go
func TestRecomputeJobStatus(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()
	user := makeTestUser(t, q, ctx, "Rita", "rita@example.com")

	mkJob := func(name string) store.Job {
		job, err := q.CreateJob(ctx, store.CreateJobParams{
			Name: name, Priority: "normal", SubmittedBy: user.ID,
			Labels: []byte(`{}`), ScheduledJobID: pgtype.UUID{},
		})
		require.NoError(t, err)
		return job
	}
	mkTask := func(job store.Job, status string) {
		task, err := q.CreateTask(ctx, store.CreateTaskParams{
			JobID: job.ID, Name: "t", Commands: []byte(`[["true"]]`),
			Env: []byte(`{}`), Requires: []byte(`{}`),
		})
		require.NoError(t, err)
		if status != "pending" {
			_, err = q.UpdateTaskStatusEpoch(ctx, store.UpdateTaskStatusEpochParams{
				ID: task.ID, Status: status, Epoch: 0,
			})
			require.NoError(t, err)
		}
	}

	// All tasks done -> job done.
	allDone := mkJob("all-done")
	mkTask(allDone, "done")
	mkTask(allDone, "done")
	got, err := q.RecomputeJobStatus(ctx, allDone.ID)
	require.NoError(t, err)
	assert.Equal(t, "done", got)

	// One still active -> job running.
	oneActive := mkJob("one-active")
	mkTask(oneActive, "done")
	mkTask(oneActive, "running")
	got, err = q.RecomputeJobStatus(ctx, oneActive.ID)
	require.NoError(t, err)
	assert.Equal(t, "running", got)

	// All terminal but one failed -> job failed (timed_out also terminal-failure).
	mixedFail := mkJob("mixed-fail")
	mkTask(mixedFail, "done")
	mkTask(mixedFail, "failed")
	mkTask(mixedFail, "timed_out")
	got, err = q.RecomputeJobStatus(ctx, mixedFail.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", got)

	// No tasks -> pgx.ErrNoRows, mirroring the old "" return.
	empty := mkJob("empty")
	_, err = q.RecomputeJobStatus(ctx, empty.ID)
	require.ErrorIs(t, err, pgx.ErrNoRows)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -tags integration -p 1 ./internal/store/... -run TestRecomputeJobStatus -v -timeout 120s`
Expected: compiles and runs against a container; assertions PASS if Task 1 was completed. (Task 1 already added the query, so this test should pass on first green run. If `RecomputeJobStatus` is undefined, Task 1's generate/commit was skipped - go back and finish Task 1.)

- [ ] **Step 3: Add the concurrent-completion ordering subtest**

This is the regression that motivates the fix: simulate two agents finishing the last two tasks, then recomputing concurrently. Whatever the interleaving, the final job status must be `done` (never stranded `running`). Append inside the same test function, after the no-tasks case:

```go
	// Concurrent completion: two tasks marked done, two goroutines recompute
	// at once. The final committed job status must be terminal, never 'running'.
	for i := 0; i < 20; i++ {
		race := mkJob("race")
		mkTask(race, "done")
		mkTask(race, "done")

		var wg sync.WaitGroup
		wg.Add(2)
		for g := 0; g < 2; g++ {
			go func() {
				defer wg.Done()
				_, _ = q.RecomputeJobStatus(ctx, race.ID)
			}()
		}
		wg.Wait()

		final, err := q.GetJob(ctx, race.ID)
		require.NoError(t, err)
		assert.Equal(t, "done", final.Status, "iteration %d stranded job", i)
	}
```

Add `"sync"` to the import block at the top of `internal/store/store_test.go` if not already present.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -tags integration -p 1 ./internal/store/... -run TestRecomputeJobStatus -v -timeout 180s`
Expected: PASS. The concurrent loop never strands the job in `running`.

- [ ] **Step 5: Commit**

```bash
git add internal/store/store_test.go
git commit -m "test: integration coverage for RecomputeJobStatus including concurrent completion"
```

---

## Task 3: Switch the worker handler to the atomic query and delete the dead copy

**Files:**
- Modify: `internal/worker/handler.go:589-618` (replace helper body)
- Modify: `internal/api/jobs.go:770-800` (delete the dead copy and its section comment)

- [ ] **Step 1: Replace the helper body in `internal/worker/handler.go`**

Replace the whole helper at lines 589-618 with:

```go
// updateJobStatusFromTasks atomically recomputes and persists a job's status
// from its tasks in a single SQL statement, so concurrent last-task completions
// can never strand the job in 'running'. Returns the new status string, or ""
// if it could not be determined (e.g. the job has no tasks).
func updateJobStatusFromTasks(ctx context.Context, q *store.Queries, jobID pgtype.UUID) string {
	status, err := q.RecomputeJobStatus(ctx, jobID)
	if err != nil {
		return ""
	}
	return status
}
```

This preserves the exact signature and the `""`-on-failure contract, so both callers (line 444 retry path, line 476 terminal-SSE path) keep working unchanged. The terminal SSE `job` event at lines 484-490 still fires because the helper still returns `"done"`/`"failed"`.

- [ ] **Step 2: Verify the worker package still compiles and check for now-unused imports**

Run: `go build ./internal/worker/...`
Expected: builds clean. The helper no longer uses `ListTasksByJob` or `UpdateJobStatus`, but other code in `handler.go` may still reference `store`; do not remove the `store` import unless the build complains. If `go build` reports an unused import, remove only that import.

- [ ] **Step 3: Delete the dead copy in `internal/api/jobs.go`**

Remove lines 770-800 in `internal/api/jobs.go`: the section comment

```go
// ─── Package-level helper ─────────────────────────────────────────────────────
```

and the entire dead `updateJobStatusFromTasks` function below it (the `:one`-less copy that returns nothing). Confirmed unused: only callers are in `internal/worker`.

- [ ] **Step 4: Verify the api package still compiles and check imports**

Run: `go build ./internal/api/...`
Expected: builds clean. If deleting the dead function orphaned an import used only by it, `go build` will flag it; remove only the flagged import. (Likely none - `store`, `context`, `pgtype` are used elsewhere in `jobs.go`.)

- [ ] **Step 5: Run the full unit build and unit tests**

Run: `go build ./...`
Expected: builds clean.

Run: `make test`
Expected: PASS (no regressions in the non-integration suite).

- [ ] **Step 6: Run the worker integration tests**

Run: `go test -tags integration -p 1 ./internal/worker/... -v -timeout 180s`
Expected: PASS. This exercises `handleTaskStatus` end to end and confirms the SSE/job-status behavior is intact through the atomic helper.

- [ ] **Step 7: Commit**

```bash
git add internal/worker/handler.go internal/api/jobs.go
git commit -m "worker: use atomic RecomputeJobStatus and delete dead api copy"
```

---

## Self-Review

**Spec coverage:**
1. New query placement (`jobs.sql`) + `make generate` + CRLF cleanup - Task 1. Done.
2. Current helper behavior / SSE preservation / return-value usage - documented in Background; helper keeps signature and `""`-on-failure contract, terminal SSE at handler.go:484-490 unchanged - Task 3. Done.
3. Invariant / transaction check - documented in Background: writes `jobs.status` (not epoch-fenced), single atomic statement, no tx needed. Done.
4. Dead-copy deletion - grep-confirmed unused, deleted in Task 3 Step 3. Done.
5. Test strategy - store-layer integration test (needs Postgres) including concurrent-completion ordering - Task 2. Done.

**Placeholder scan:** No TBD/TODO; every code step shows real code and exact commands.

**Type consistency:** `RecomputeJobStatus(ctx, jobID pgtype.UUID) (string, error)` is used identically in Tasks 1, 2, and 3. Helper `updateJobStatusFromTasks` keeps its original `(ctx, *store.Queries, pgtype.UUID) string` signature, so callers at handler.go:444 and :476 need no edits.

**Edge case noted:** `RecomputeJobStatus` returns `pgx.ErrNoRows` for a job with no tasks (via `HAVING COUNT(*) > 0`); the helper maps that to `""`, matching the old `len(tasks) == 0` branch.
