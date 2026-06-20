# Schedrunner Poisoned Tick + Reconcile last_run_at Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop one poisoned schedule from aborting the whole schedrunner tick (and hot-looping every 10s), and stop `ReconcileOnStartup` from recording `last_run_at` for runs that never happened.

**Architecture:** Wrap each per-row fire in a pgx savepoint (nested transaction) so a failing schedule rolls back only its own writes, leaving the outer batch tx usable; after rolling back the savepoint we still advance `next_run_at` on the outer tx so the bad schedule does not retry forever. Add a `last_run_at`-free advance query (`AdvanceScheduledJobNextRun`) used by the failure path and by `ReconcileOnStartup`, while the success path keeps the existing `AdvanceScheduledJob` (which sets `last_run_at`).

**Tech Stack:** Go, pgx/v5, sqlc, Postgres, testcontainers-go.

**Slice independence:** BACKEND-ONLY. No frontend slice. All work is in `internal/schedrunner` and `internal/store`. There is no Phase 3 parallelism to declare.

---

## Background: verified current code (real line numbers)

`internal/schedrunner/runner.go` (147 lines total):

- `TickOnce` (lines 56-72): opens ONE outer tx `r.pool.Begin(ctx)` (57), `defer tx.Rollback` (61), `q := r.q.WithTx(tx)` (62), `ListEligibleScheduledJobs` (64), loops `r.fireOne(ctx, q, row)` (68-70), `tx.Commit(ctx)` (71). The stale comment at lines 51-55 claims a failed fire "still advances next_run_at via the same tx" - this is the bug: a Postgres error aborts the whole tx.
- `fireOne` (lines 74-109): swallows all errors (logs + returns nothing). Calls `r.advance(...)` on the same `q` for each terminal path. The fire write is `jobcreate.CreateJobFromSpec(ctx, q, spec, row.OwnerID, row.ID)` (line 102).
- `advance` (lines 111-119): calls `q.AdvanceScheduledJob` (sets `last_run_at = NOW()`).
- `ReconcileOnStartup` (lines 124-146): loops overdue rows, calls `q.AdvanceScheduledJob` (line 137) - falsely sets `last_run_at`.

`internal/store/query/scheduled_jobs.sql`:

- `AdvanceScheduledJob :exec` (lines 61-67): sets `next_run_at = $2, last_run_at = NOW(), last_job_id = COALESCE($3, last_job_id), updated_at = NOW()`.

`internal/jobcreate/jobcreate.go:25`: `CreateJobFromSpec(ctx, q, spec, submittedBy, scheduledID) (store.Job, []store.Task, error)` - caller owns Begin/Commit; takes any `*store.Queries`.

`internal/store/db.go:28`: `(q *Queries) WithTx(tx pgx.Tx) *Queries` - rebinds queries to a tx (or savepoint, which also satisfies `pgx.Tx`).

Integration test harness: `internal/schedrunner/runner_test.go` (build tag `//go:build integration`) has `newRunnerHarness(t)` (spins up Postgres via testcontainers, runs migrations, returns `{pool, q}`), `createUser`, `makeSpecJSON`. Extend this file.

### pgx savepoint semantics (the load-bearing detail)

Calling `tx.Begin(ctx)` on an already-open pgx `Tx` does NOT open a new DB transaction; it returns a pseudo-nested transaction that issues `SAVEPOINT`. On that nested value:
- `sp.Commit(ctx)` issues `RELEASE SAVEPOINT`.
- `sp.Rollback(ctx)` issues `ROLLBACK TO SAVEPOINT`, which clears the aborted-statement error and leaves the OUTER tx usable for subsequent statements and the final `Commit`.

This is exactly what we need: a poisoned `CreateJobFromSpec` aborts only the savepoint; we roll it back and the outer tx survives to commit the healthy schedules.

### Decided control flow for the new `fireOne`

`fireOne` returns `error`. The savepoint wrapping lives in `TickOnce`, not `fireOne`, so that `fireOne` operates purely on the savepoint-bound `*store.Queries` and the advance-on-failure happens on the OUTER tx. Precise placement:

- Per row in the loop, `TickOnce` opens a savepoint `sp`, binds `spq := r.q.WithTx(sp)`, and calls `r.fireOne(ctx, spq, row)`.
- On `fireOne` success: `sp.Commit(ctx)` (RELEASE). The advance for the success path happens INSIDE `fireOne` (on `spq`) because it must be atomic with the job creation - a created job and its `last_run_at`/`last_job_id` belong together.
- On `fireOne` failure: `sp.Rollback(ctx)` (ROLLBACK TO SAVEPOINT), then advance `next_run_at` ONLY (no `last_run_at`) on the OUTER tx via `AdvanceScheduledJobNextRun`, using the per-schedule next-fire time when known or `time.Now().Add(time.Minute)` as the existing fallback for unparseable rows.

Rationale: putting the success advance inside the savepoint keeps create+advance atomic; putting the failure advance on the outer tx is what prevents both the hot-loop (next_run_at still moves) and the batch abort (savepoint already rolled back, outer tx clean). The failure path must NOT set `last_run_at` because no job ran.

A subtlety: `fireOne` needs to communicate the intended `next_run_at` to the failure path so the outer-tx advance uses the right timestamp. We pass the computed next-fire back via a small return signature change: `fireOne` returns `(nextOnFailure time.Time, err error)`. On success `err == nil` and the caller does nothing further. On failure the caller advances to `nextOnFailure`. For the early unparseable-spec / bad-cron paths `fireOne` returns `time.Now().Add(time.Minute)` as the failure target (matching current behavior at runner.go:78,84).

---

## Task 1: Add `AdvanceScheduledJobNextRun` sqlc query

**Files:**
- Modify: `internal/store/query/scheduled_jobs.sql` (insert after `AdvanceScheduledJob`, currently lines 61-67)
- Generated (do NOT hand-edit): `internal/store/scheduled_jobs.sql.go`

- [ ] **Step 1: Add the new query to the .sql file**

Insert immediately after the existing `AdvanceScheduledJob` block (after line 67), before `CountActiveJobsForSchedule`:

```sql
-- name: AdvanceScheduledJobNextRun :exec
UPDATE scheduled_jobs
SET next_run_at = $2,
    updated_at  = NOW()
WHERE id = $1;
```

- [ ] **Step 2: Regenerate the store layer**

Run: `make generate`
Expected: `internal/store/scheduled_jobs.sql.go` now contains an `AdvanceScheduledJobNextRun` constant + `AdvanceScheduledJobNextRunParams` struct (fields `ID pgtype.UUID`, `NextRunAt pgtype.Timestamptz`) + method `(q *Queries) AdvanceScheduledJobNextRun(ctx, arg) error`.

- [ ] **Step 3: Clean up sqlc CRLF churn (per CLAUDE.md)**

sqlc emits LF; this repo is CRLF, so generation rewrites line endings across generated files. Run:

```bash
git diff --ignore-all-space --stat
```

For every generated file whose only change is line endings (no real content delta under `--ignore-all-space`), revert it:

```bash
git checkout -- <file-with-only-LF-churn>
```

Keep ONLY `internal/store/scheduled_jobs.sql.go` (the real content change) and the `.sql` edit. Confirm with:

```bash
git diff --ignore-all-space -- internal/store/scheduled_jobs.sql.go
```
Expected: shows the new `AdvanceScheduledJobNextRun` additions and nothing unrelated.

- [ ] **Step 4: Build to confirm the generated code compiles**

Run: `go build ./internal/store/...`
Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add internal/store/query/scheduled_jobs.sql internal/store/scheduled_jobs.sql.go
git commit -m "store: add AdvanceScheduledJobNextRun query (next_run_at only)"
```

---

## Task 2: Switch ReconcileOnStartup to the last_run_at-free advance

This is the smaller, self-contained half of the bug and has a clean integration test. Do it before the savepoint refactor.

**Files:**
- Modify: `internal/schedrunner/runner.go:137-141` (the `AdvanceScheduledJob` call inside `ReconcileOnStartup`)
- Test: `internal/schedrunner/runner_test.go` (extend existing reconcile test or add a new one)

- [ ] **Step 1: Write the failing integration test**

Add to `internal/schedrunner/runner_test.go` (it already has the `integration` build tag and harness):

```go
func TestRunner_ReconcileOnStartup_DoesNotSetLastRunAt(t *testing.T) {
	h := newRunnerHarness(t)
	ctx := context.Background()
	userID := h.createUser(t, "alice-reconcile-lastrun@example.com")

	oldNext := time.Now().Add(-25 * time.Hour)
	sj, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name:          "nightly",
		OwnerID:       userID,
		CronExpr:      "0 2 * * *",
		Timezone:      "UTC",
		JobSpec:       makeSpecJSON(t),
		OverlapPolicy: "skip",
		Enabled:       true,
		NextRunAt:     pgtype.Timestamptz{Time: oldNext, Valid: true},
	})
	require.NoError(t, err)

	// Sanity: freshly created schedule has no last_run_at.
	before, err := h.q.GetScheduledJob(ctx, sj.ID)
	require.NoError(t, err)
	require.False(t, before.LastRunAt.Valid, "precondition: last_run_at unset on create")

	require.NoError(t, schedrunner.ReconcileOnStartup(ctx, h.q))

	row, err := h.q.GetScheduledJob(ctx, sj.ID)
	require.NoError(t, err)
	require.True(t, row.NextRunAt.Time.After(time.Now()), "next_run_at should advance")
	require.False(t, row.LastRunAt.Valid, "reconcile must NOT set last_run_at for a run that never happened")
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -tags integration -p 1 ./internal/schedrunner/... -run TestRunner_ReconcileOnStartup_DoesNotSetLastRunAt -v -timeout 180s`
Expected: FAIL on the final assertion - `last_run_at` IS set because `ReconcileOnStartup` currently calls `AdvanceScheduledJob` (which sets `last_run_at = NOW()`).

- [ ] **Step 3: Switch ReconcileOnStartup to AdvanceScheduledJobNextRun**

In `internal/schedrunner/runner.go`, replace the `AdvanceScheduledJob` call inside `ReconcileOnStartup` (currently lines 137-141) with:

```go
		if err := q.AdvanceScheduledJobNextRun(ctx, store.AdvanceScheduledJobNextRunParams{
			ID:        row.ID,
			NextRunAt: pgtype.Timestamptz{Time: next, Valid: true},
		}); err != nil {
			log.Printf("schedrunner: reconcile advance for %s: %v", row.Name, err)
		}
```

(The `LastJobID` field is gone - the new params struct has only `ID` and `NextRunAt`.)

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -tags integration -p 1 ./internal/schedrunner/... -run TestRunner_ReconcileOnStartup -v -timeout 180s`
Expected: PASS for both `_AdvancesPastMissedTriggers` and `_DoesNotSetLastRunAt`.

- [ ] **Step 5: Commit**

```bash
git add internal/schedrunner/runner.go internal/schedrunner/runner_test.go
git commit -m "schedrunner: reconcile advances next_run_at without falsifying last_run_at"
```

---

## Task 3: Make fireOne return its failure-advance target and an error

Pure refactor of `fireOne` - no behavior change yet (caller still advances on the same `q`). This isolates the signature change so the savepoint task stays small. Verified by the existing integration suite continuing to pass.

**Files:**
- Modify: `internal/schedrunner/runner.go` - `fireOne` (74-109) and its caller in `TickOnce` (68-70)

- [ ] **Step 1: Change fireOne signature and returns**

Replace `fireOne` (lines 74-109) with the version below. It computes the spec/cron, returns `(time.Time, error)` from every path, and on success advances inside itself on the passed `q`. The `time.Time` is the next_run_at the FAILURE path should use; callers ignore it when `err == nil`.

```go
// fireOne attempts to fire one schedule using q. On success it creates the job
// AND advances the schedule (last_run_at + last_job_id) on q, then returns a nil
// error. On failure it returns the next_run_at the caller should advance to
// (without setting last_run_at) and a non-nil error. The caller is responsible
// for the savepoint and the failure-path advance on the outer tx.
func (r *Runner) fireOne(ctx context.Context, q *store.Queries, row store.ScheduledJob) (time.Time, error) {
	var spec jobspec.JobSpec
	if err := json.Unmarshal(row.JobSpec, &spec); err != nil {
		return time.Now().Add(time.Minute), fmt.Errorf("invalid job_spec: %w", err)
	}
	sched, err := ParseSchedule(row.CronExpr, row.Timezone)
	if err != nil {
		return time.Now().Add(time.Minute), fmt.Errorf("parse cron: %w", err)
	}
	nextFire := sched.Next(time.Now())

	if row.OverlapPolicy == "skip" {
		active, err := q.CountActiveJobsForSchedule(ctx, row.ID)
		if err != nil {
			return nextFire, fmt.Errorf("count active jobs: %w", err)
		}
		if active > 0 {
			log.Printf("schedrunner: skipping schedule %s (previous run still active)", row.Name)
			r.advance(ctx, q, row, pgtype.UUID{}, nextFire)
			return nextFire, nil
		}
	}

	job, _, err := jobcreate.CreateJobFromSpec(ctx, q, spec, row.OwnerID, row.ID)
	if err != nil {
		return nextFire, fmt.Errorf("create job: %w", err)
	}
	r.advance(ctx, q, row, job.ID, nextFire)
	return nextFire, nil
}
```

Note: the "skip" branch is a SUCCESS path (no job created, but a legitimate advance with `last_run_at`/no-job - matching current behavior at runner.go:97 which set `last_job_id` invalid). It returns `nil` so the caller commits the savepoint.

- [ ] **Step 2: Add the `fmt` import**

In the import block at the top of `internal/schedrunner/runner.go`, add `"fmt"` (currently the block has `context`, `encoding/json`, `log`, `time`). Result:

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"relay/internal/jobcreate"
	"relay/internal/jobspec"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)
```

- [ ] **Step 3: Update the TickOnce caller to consume the returns (temporary, no savepoint yet)**

In `TickOnce`, replace the loop body (lines 68-70) with a version that logs the error and advances on failure on the SAME `q` (no savepoint yet - this preserves current single-tx behavior so this task is a pure refactor):

```go
	for _, row := range rows {
		next, err := r.fireOne(ctx, q, row)
		if err != nil {
			log.Printf("schedrunner: fire schedule %s: %v", row.Name, err)
			r.advanceNextRun(ctx, q, row, next)
		}
	}
```

- [ ] **Step 4: Add the advanceNextRun helper**

Add next to `advance` (after line 119):

```go
func (r *Runner) advanceNextRun(ctx context.Context, q *store.Queries, row store.ScheduledJob, next time.Time) {
	if err := q.AdvanceScheduledJobNextRun(ctx, store.AdvanceScheduledJobNextRunParams{
		ID:        row.ID,
		NextRunAt: pgtype.Timestamptz{Time: next, Valid: true},
	}); err != nil {
		log.Printf("schedrunner: AdvanceScheduledJobNextRun for %s: %v", row.Name, err)
	}
}
```

- [ ] **Step 5: Build and run the existing integration suite to confirm no regression**

Run: `go build ./internal/schedrunner/...`
Expected: no errors.

Run: `go test -tags integration -p 1 ./internal/schedrunner/... -run TestRunner -v -timeout 240s`
Expected: PASS for all existing `TestRunner_*` tests (Fires, OverlapSkip, Reconcile, Source).

- [ ] **Step 6: Commit**

```bash
git add internal/schedrunner/runner.go
git commit -m "schedrunner: fireOne returns error and failure-advance target"
```

---

## Task 4: Wrap each fire in a savepoint so one poisoned schedule cannot abort the batch

This is the core fix. The failing test must demonstrate that a poisoned schedule does NOT prevent a healthy schedule in the same tick from committing, AND the poisoned schedule still advances (no hot-loop).

**Files:**
- Modify: `internal/schedrunner/runner.go` - `TickOnce` (56-72)
- Test: `internal/schedrunner/runner_test.go`

### Poisoning strategy

We need a schedule whose `fireOne` fails at the DB layer (so it would abort the tx without a savepoint), is deterministic, and persists across ticks. The cleanest poison: a valid-looking spec whose `CreateJobFromSpec` -> `CreateJob` violates a NOT NULL / FK constraint. The reliable lever is `owner_id`: insert a scheduled job row directly with an `owner_id` that does not exist in `users`, so `CreateJob`'s `submitted_by` FK insert fails inside the savepoint.

Verify the FK before writing the test: `submitted_by` on `jobs` references `users(id)`. If `CreateScheduledJob` itself enforces an `owner_id` FK (preventing creation of the poison row), fall back to inserting the poison row with `INSERT ... ` raw SQL via `h.pool.Exec`, bypassing the `CreateScheduledJob` query, OR use a spec that passes `jobspec.Validate` but fails at insert. Confirm which by reading `internal/store/migrations` for the `scheduled_jobs.owner_id` and `jobs.submitted_by` constraints during Step 0.

- [ ] **Step 0: Confirm the poison lever**

Run:
```bash
rg -n "owner_id|submitted_by|REFERENCES" internal/store/migrations
```
Expected: identify whether `scheduled_jobs.owner_id` has an FK (if so the harness must insert the poison row with a real user but make the FIRE fail another way) and that `jobs.submitted_by` references `users(id)`. Decide the poison: if `scheduled_jobs.owner_id` has NO FK, use a non-existent `owner_id` so `CreateJob` fails on the `submitted_by` FK. If it DOES have an FK, instead poison via a real user but a spec that inserts a duplicate/over-long value - e.g. a task `name` exceeding a column limit, or reuse a real user and force a unique-constraint hit. Record the chosen lever in the test comment.

- [ ] **Step 1: Write the failing integration test**

Add to `internal/schedrunner/runner_test.go`. This version assumes `scheduled_jobs.owner_id` has no FK (poison via bogus owner). Adjust the poison per Step 0 if needed. Add a raw-insert helper if `CreateScheduledJob` rejects the bogus owner.

```go
func TestRunner_PoisonedScheduleDoesNotAbortHealthyOne(t *testing.T) {
	h := newRunnerHarness(t)
	ctx := context.Background()
	healthyOwner := h.createUser(t, "healthy@example.com")

	overdue := pgtype.Timestamptz{Time: time.Now().Add(-1 * time.Second), Valid: true}

	// Poison: bogus owner_id so CreateJob's submitted_by FK insert fails
	// inside the savepoint. It sorts FIRST (older next_run_at) to prove it
	// does not starve the healthy one.
	bogusOwner := pgtype.UUID{Bytes: [16]byte{0xde, 0xad}, Valid: true}
	poison, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name:          "poison",
		OwnerID:       bogusOwner,
		CronExpr:      "@hourly",
		Timezone:      "UTC",
		JobSpec:       makeSpecJSON(t),
		OverlapPolicy: "skip",
		Enabled:       true,
		NextRunAt:     pgtype.Timestamptz{Time: time.Now().Add(-10 * time.Second), Valid: true},
	})
	require.NoError(t, err)

	healthy, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name:          "healthy",
		OwnerID:       healthyOwner,
		CronExpr:      "@hourly",
		Timezone:      "UTC",
		JobSpec:       makeSpecJSON(t),
		OverlapPolicy: "skip",
		Enabled:       true,
		NextRunAt:     overdue,
	})
	require.NoError(t, err)

	runner := schedrunner.NewRunner(h.pool, h.q)
	require.NoError(t, runner.TickOnce(ctx), "tick must commit despite one poisoned schedule")

	// Healthy schedule committed its job + advance.
	healthyJobs, err := h.q.ListJobsByScheduledJob(ctx, healthy.ID)
	require.NoError(t, err)
	require.Len(t, healthyJobs, 1, "healthy schedule must commit its job")

	healthyRow, err := h.q.GetScheduledJob(ctx, healthy.ID)
	require.NoError(t, err)
	require.True(t, healthyRow.NextRunAt.Time.After(time.Now()), "healthy next_run_at must advance")
	require.True(t, healthyRow.LastRunAt.Valid, "healthy last_run_at must be set")

	// Poison schedule created no job but still advanced (no hot-loop), and
	// did NOT falsely record last_run_at.
	poisonJobs, err := h.q.ListJobsByScheduledJob(ctx, poison.ID)
	require.NoError(t, err)
	require.Len(t, poisonJobs, 0, "poison schedule must create no job")

	poisonRow, err := h.q.GetScheduledJob(ctx, poison.ID)
	require.NoError(t, err)
	require.True(t, poisonRow.NextRunAt.Time.After(time.Now()), "poison next_run_at must advance so it does not hot-loop")
	require.False(t, poisonRow.LastRunAt.Valid, "poison last_run_at must stay unset (no run happened)")
}
```

If `CreateScheduledJob` rejects `bogusOwner` due to an FK (per Step 0), add this helper and use it for the poison row instead:

```go
func (h *runnerHarness) insertScheduledJobRaw(t *testing.T, name string, owner pgtype.UUID, spec []byte, next time.Time) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	err := h.pool.QueryRow(context.Background(),
		`INSERT INTO scheduled_jobs (name, owner_id, cron_expr, timezone, job_spec, overlap_policy, enabled, next_run_at)
		 VALUES ($1,$2,'@hourly','UTC',$3,'skip',true,$4) RETURNING id`,
		name, owner, spec, next).Scan(&id)
	require.NoError(t, err)
	return id
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -tags integration -p 1 ./internal/schedrunner/... -run TestRunner_PoisonedScheduleDoesNotAbortHealthyOne -v -timeout 180s`
Expected: FAIL. Without the savepoint, the poison `CreateJob` aborts the outer tx, so `tx.Commit` returns an error (`require.NoError(t, runner.TickOnce(...))` fails) - or if commit is reached, the healthy job assertion fails because nothing committed.

- [ ] **Step 3: Wrap each fire in a savepoint in TickOnce**

Replace the loop in `TickOnce` (lines 68-70, including the version from Task 3 Step 3) with the savepoint-per-row version. Keep the outer tx Begin/Commit unchanged.

```go
	for _, row := range rows {
		sp, err := tx.Begin(ctx)
		if err != nil {
			log.Printf("schedrunner: begin savepoint for %s: %v", row.Name, err)
			continue
		}
		next, fireErr := r.fireOne(ctx, r.q.WithTx(sp), row)
		if fireErr != nil {
			// Roll back ONLY this schedule's writes; the outer tx stays usable.
			if rbErr := sp.Rollback(ctx); rbErr != nil {
				log.Printf("schedrunner: rollback savepoint for %s: %v", row.Name, rbErr)
			}
			log.Printf("schedrunner: fire schedule %s: %v", row.Name, fireErr)
			// Advance next_run_at on the OUTER tx (no last_run_at) so the
			// poisoned schedule stops hot-looping every tick.
			r.advanceNextRun(ctx, q, row, next)
			continue
		}
		if err := sp.Commit(ctx); err != nil {
			log.Printf("schedrunner: release savepoint for %s: %v", row.Name, err)
		}
	}
```

- [ ] **Step 4: Update the stale TickOnce doc comment**

Replace the comment at lines 51-55 (the one claiming "a failed fire still advances next_run_at via the same tx") with:

```go
// TickOnce performs one poll-and-fire cycle. Exposed for testing.
//
// All eligible rows in a tick share one outer transaction, but each row's fire
// runs inside its own savepoint (pgx nested tx). A failed fire rolls back only
// its savepoint, so a single poisoned schedule cannot abort the healthy rows'
// commits. The failed schedule's next_run_at is still advanced on the outer tx
// (without setting last_run_at) so it does not hot-loop every tick.
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test -tags integration -p 1 ./internal/schedrunner/... -run TestRunner_PoisonedScheduleDoesNotAbortHealthyOne -v -timeout 180s`
Expected: PASS.

- [ ] **Step 6: Run the full schedrunner integration suite for regressions**

Run: `go test -tags integration -p 1 ./internal/schedrunner/... -run TestRunner -v -timeout 300s`
Expected: PASS for all `TestRunner_*` tests.

- [ ] **Step 7: Commit**

```bash
git add internal/schedrunner/runner.go internal/schedrunner/runner_test.go
git commit -m "schedrunner: savepoint per fire so one poisoned schedule cannot abort the batch"
```

---

## Task 5: Final verification

- [ ] **Step 1: Unit tests + vet (no Docker)**

Run: `make test`
Expected: PASS (the schedrunner unit tests `cron_test.go` are unaffected; the integration tests are gated out).

Run: `go vet ./internal/schedrunner/... ./internal/store/...`
Expected: no findings.

- [ ] **Step 2: Full schedrunner integration suite, clean run**

Run: `go test -tags integration -p 1 ./internal/schedrunner/... -v -timeout 300s`
Expected: all PASS, including the two new tests (`_DoesNotSetLastRunAt`, `_PoisonedScheduleDoesNotAbortHealthyOne`).

- [ ] **Step 3: Confirm no stray generated-file churn**

Run: `git status --short` and `git diff --ignore-all-space --stat`
Expected: only the four intended files touched across commits (`scheduled_jobs.sql`, `scheduled_jobs.sql.go`, `runner.go`, `runner_test.go`); no LF-only churn left in other generated files.

---

## Self-Review

- **Spec coverage:**
  - Poisoned-tick abort fixed: Task 4 (savepoint per row) + Task 3 (fireOne returns error). Tested by `TestRunner_PoisonedScheduleDoesNotAbortHealthyOne`.
  - Hot-loop fixed: failure path advances `next_run_at` on outer tx (Task 4 Step 3); asserted by the poison test's `poisonRow.NextRunAt ... After(now)`.
  - Reconcile falsifying `last_run_at` fixed: Task 2; asserted by `_DoesNotSetLastRunAt`.
  - New `AdvanceScheduledJobNextRun :exec`: Task 1, with `make generate` + CRLF cleanup.
  - `fireOne` returns an error instead of swallowing: Task 3.
- **Placeholder scan:** every code step shows real code; no TODO/TBD.
- **Type consistency:** `AdvanceScheduledJobNextRunParams{ID, NextRunAt}` used identically in Task 2 (reconcile), Task 3 (`advanceNextRun` helper), and Task 4 (outer-tx failure advance). `fireOne` returns `(time.Time, error)` and is called with that signature in Task 3 Step 3 and Task 4 Step 3. `advanceNextRun` defined in Task 3 Step 4, reused in Task 4.
- **Ordering / invariants:** Task 1 (.sql) includes `make generate`. Generated `*.sql.go` is never hand-edited. The epoch-fence invariant is untouched (no writes to `tasks.status`/`task_logs` here). The single job-spec pipeline is preserved - `CreateJobFromSpec` remains the only fire path; we only wrap it in a savepoint.
