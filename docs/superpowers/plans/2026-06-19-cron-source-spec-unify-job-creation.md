# Cron Source Spec - Unify Job Creation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make cron-fired scheduled jobs persist task `source` specs identically to run-now and `POST /v1/jobs`, by eliminating schedrunner's parallel hand-rolled job-creation path and routing all creation through one shared `CreateJobFromSpec`.

**Architecture:** Move `CreateJobFromSpec` verbatim out of `internal/api/job_spec.go` into a new dependency-light package `internal/jobcreate` (imports only `internal/jobspec` + `internal/store` + pgx). `internal/api` keeps its `CreateJobFromSpec` as a thin wrapper that delegates to `jobcreate`, so all api callers and tests stay green. `internal/schedrunner` deletes its `runnerSpec`/`runnerTaskSpec`/`Runner.createJob` and calls `jobcreate.CreateJobFromSpec` after unmarshalling the stored `job_spec` into a `jobspec.JobSpec`. This breaks the (real) `api -> schedrunner` cycle because both packages already import `store`/`jobspec`.

**Tech Stack:** Go, sqlc-generated `internal/store`, pgx/pgtype, testcontainers-go Postgres integration tests (`//go:build integration`).

**Slice independence:** This is BACKEND-ONLY work. There is no frontend slice. All tasks are sequential within a single backend engineer + reviewer loop (Task 3's red test depends on Task 1's new package existing to compile against; Task 4's green relies on Task 1 + Task 5; the wrapper in Task 2 must land before/with the move so api stays green). There is no Phase 3 frontend/backend parallelism to declare.

**Invariants respected:**
- **Single job-spec pipeline** (CLAUDE.md): this change collapses the last parallel creation path (schedrunner) onto `jobspec.Validate` + `CreateJobFromSpec`. No new spec structs or task-creation paths are introduced; `runnerSpec`/`runnerTaskSpec` are deleted.
- **Epoch fence** (CLAUDE.md): unchanged. No writes to `tasks.status` or `task_logs` are added or altered; task creation uses the same `CreateTask`/`CreateTaskWithSource` queries as today.

**Constraints (from spec):**
- No DB migration, no new sqlc query, no proto change. Do NOT run `make generate`.
- Surgical changes only; match existing style. No unrelated refactoring.
- TDD: the schedrunner source-persistence integration test must be written and confirmed RED on current code before the implementation that makes it green.

---

## Critical files

- `internal/api/job_spec.go` - source of the canonical `CreateJobFromSpec` body to move; becomes a thin wrapper.
- `internal/schedrunner/runner.go` - holds the duplicate `runnerSpec`/`runnerTaskSpec`/`Runner.createJob` to delete; `fireOne` rewires to `jobcreate`.
- `internal/jobspec/jobspec.go` - canonical `JobSpec`/`TaskSpec`/`SourceSpec` types + `Validate` (already does the legacy `command -> commands` collapse). Read-only here.
- `internal/jobcreate/jobcreate.go` - NEW; receives the moved `CreateJobFromSpec`.
- `internal/schedrunner/runner_test.go` - NEW source-persistence integration test added here.
- `internal/api/job_spec_test.go` - existing api tests that must stay green via the wrapper (read-only).

## File structure

- **Create** `internal/jobcreate/jobcreate.go` - one responsibility: insert a job + its tasks + dependencies from a validated `jobspec.JobSpec`, emitting `NotifyTaskSubmitted`. Imports `context`, `encoding/json`, `fmt`, `relay/internal/jobspec`, `relay/internal/store`, `github.com/jackc/pgx/v5/pgtype`.
- **Modify** `internal/api/job_spec.go` - `CreateJobFromSpec` body shrinks to a one-line delegate to `jobcreate.CreateJobFromSpec`. Aliases (`JobSpec`/`TaskSpec`/`SourceSpec`/`SyncEntry`) and `ValidateJobSpec` are untouched.
- **Modify** `internal/schedrunner/runner.go` - delete `runnerSpec`, `runnerTaskSpec`, `Runner.createJob`; rewrite `fireOne` to unmarshal into `jobspec.JobSpec` and call `jobcreate.CreateJobFromSpec`.
- **Modify** `internal/schedrunner/runner_test.go` - add the red-then-green source-persistence test.

---

## Task 1: Create the shared `internal/jobcreate` package

Move `CreateJobFromSpec` verbatim from `internal/api/job_spec.go` into a new package. The only behavioral change versus the api copy: call `jobspec.Validate(&spec)` directly instead of the api-local `ValidateJobSpec` helper (they are identical - `ValidateJobSpec` just wraps `jobspec.Validate(&spec)`).

**Files:**
- Create: `internal/jobcreate/jobcreate.go`

- [ ] **Step 1: Create the package file**

Create `internal/jobcreate/jobcreate.go` with the full moved function:

```go
// Package jobcreate inserts jobs, their tasks, and task dependencies from a
// validated jobspec.JobSpec. It is the single DB-touching job-creation path
// shared by the REST API (internal/api), run-now, and the cron scheduler
// (internal/schedrunner). Keeping it out of internal/jobspec preserves that
// package's purity (validation/types only) so internal/mcp can import jobspec
// without pulling in store + pgx.
package jobcreate

import (
	"context"
	"encoding/json"
	"fmt"

	"relay/internal/jobspec"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
)

// CreateJobFromSpec inserts a job, its tasks, and task dependencies inside the
// provided (transactional) Queries. Caller owns Begin/Commit. Emits
// NotifyTaskSubmitted on success.
//
// If scheduledID is a valid UUID, the resulting job.scheduled_job_id is set.
func CreateJobFromSpec(
	ctx context.Context,
	q *store.Queries,
	spec jobspec.JobSpec,
	submittedBy pgtype.UUID,
	scheduledID pgtype.UUID,
) (store.Job, []store.Task, error) {
	if err := jobspec.Validate(&spec); err != nil {
		return store.Job{}, nil, err
	}

	priority := spec.Priority
	if priority == "" {
		priority = "normal"
	}

	labelsJSON, err := json.Marshal(spec.Labels)
	if err != nil {
		return store.Job{}, nil, fmt.Errorf("marshal labels: %w", err)
	}

	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name:           spec.Name,
		Priority:       priority,
		SubmittedBy:    submittedBy,
		Labels:         labelsJSON,
		ScheduledJobID: scheduledID,
	})
	if err != nil {
		return store.Job{}, nil, fmt.Errorf("create job: %w", err)
	}

	nameToID := make(map[string]pgtype.UUID, len(spec.Tasks))
	tasks := make([]store.Task, 0, len(spec.Tasks))
	for _, ts := range spec.Tasks {
		envJSON, err := json.Marshal(ts.Env)
		if err != nil {
			return store.Job{}, nil, fmt.Errorf("marshal env for %s: %w", ts.Name, err)
		}
		requiresJSON, err := json.Marshal(ts.Requires)
		if err != nil {
			return store.Job{}, nil, fmt.Errorf("marshal requires for %s: %w", ts.Name, err)
		}
		commandsJSON, err := json.Marshal(ts.Commands)
		if err != nil {
			return store.Job{}, nil, fmt.Errorf("marshal commands for %s: %w", ts.Name, err)
		}
		var task store.Task
		var taskErr error
		if ts.Source != nil {
			sourceJSON, merr := json.Marshal(ts.Source)
			if merr != nil {
				return store.Job{}, nil, fmt.Errorf("marshal source for %s: %w", ts.Name, merr)
			}
			task, taskErr = q.CreateTaskWithSource(ctx, store.CreateTaskWithSourceParams{
				JobID:          job.ID,
				Name:           ts.Name,
				Commands:       commandsJSON,
				Env:            envJSON,
				Requires:       requiresJSON,
				TimeoutSeconds: ts.TimeoutSeconds,
				Retries:        ts.Retries,
				Source:         sourceJSON,
			})
		} else {
			task, taskErr = q.CreateTask(ctx, store.CreateTaskParams{
				JobID:          job.ID,
				Name:           ts.Name,
				Commands:       commandsJSON,
				Env:            envJSON,
				Requires:       requiresJSON,
				TimeoutSeconds: ts.TimeoutSeconds,
				Retries:        ts.Retries,
			})
		}
		if taskErr != nil {
			return store.Job{}, nil, fmt.Errorf("create task %s: %w", ts.Name, taskErr)
		}
		nameToID[ts.Name] = task.ID
		tasks = append(tasks, task)
	}

	for _, ts := range spec.Tasks {
		for _, depName := range ts.DependsOn {
			if err := q.CreateTaskDependency(ctx, store.CreateTaskDependencyParams{
				TaskID:          nameToID[ts.Name],
				DependsOnTaskID: nameToID[depName],
			}); err != nil {
				return store.Job{}, nil, fmt.Errorf("create dependency %s->%s: %w", ts.Name, depName, err)
			}
		}
	}

	_ = q.NotifyTaskSubmitted(ctx)

	return job, tasks, nil
}
```

- [ ] **Step 2: Build the new package**

Run: `go build ./internal/jobcreate/...`
Expected: PASS (compiles cleanly; no import cycle).

- [ ] **Step 3: Commit**

```bash
git add internal/jobcreate/jobcreate.go
git commit -m "refactor: add internal/jobcreate with shared CreateJobFromSpec"
```

---

## Task 2: Make `internal/api.CreateJobFromSpec` a thin wrapper

Replace the moved function body in `internal/api/job_spec.go` with a one-line delegate. Keep the exact `api.CreateJobFromSpec` signature so `jobs.go:210`, `scheduled_jobs.go:660`, and `job_spec_test.go` stay green. Keep the `JobSpec`/`TaskSpec`/`SourceSpec`/`SyncEntry` aliases and `ValidateJobSpec` unchanged.

**Files:**
- Modify: `internal/api/job_spec.go` (replace lines 27-128, the `CreateJobFromSpec` doc comment + body; keep lines 1-26 - package, imports, aliases, `ValidateJobSpec` - except adjust imports as below)

- [ ] **Step 1: Replace the function and fix imports**

Overwrite `internal/api/job_spec.go` with:

```go
package api

import (
	"context"

	"relay/internal/jobcreate"
	"relay/internal/jobspec"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
)

// Type aliases — kept so existing api code (handlers, schedrunner) compiles without changes.
type (
	JobSpec    = jobspec.JobSpec
	TaskSpec   = jobspec.TaskSpec
	SourceSpec = jobspec.SourceSpec
	SyncEntry  = jobspec.SyncEntry
)

// ValidateJobSpec preserves existing call sites (takes value, not pointer).
func ValidateJobSpec(spec JobSpec) error {
	return jobspec.Validate(&spec)
}

// CreateJobFromSpec inserts a job, its tasks, and task dependencies inside the
// provided (transactional) Queries. Caller owns Begin/Commit. Emits
// NotifyTaskSubmitted on success.
//
// If scheduledID is a valid UUID, the resulting job.scheduled_job_id is set.
//
// This delegates to jobcreate.CreateJobFromSpec, the single shared creation
// path used by the REST API, run-now, and the cron scheduler.
func CreateJobFromSpec(
	ctx context.Context,
	q *store.Queries,
	spec JobSpec,
	submittedBy pgtype.UUID,
	scheduledID pgtype.UUID,
) (store.Job, []store.Task, error) {
	return jobcreate.CreateJobFromSpec(ctx, q, spec, submittedBy, scheduledID)
}
```

Note: `encoding/json` and `fmt` are dropped from the api file's imports because the body that used them moved out. `JobSpec` is an alias for `jobspec.JobSpec`, so passing `spec` (type `JobSpec`) to `jobcreate.CreateJobFromSpec` (which takes `jobspec.JobSpec`) type-checks without conversion.

- [ ] **Step 2: Build the api package**

Run: `go build ./internal/api/...`
Expected: PASS. If it fails with "imported and not used: fmt" or similar, an import was left behind - the import block above is the complete correct set.

- [ ] **Step 3: Run existing api unit tests (no Docker)**

Run: `go test ./internal/api/... -timeout 60s`
Expected: PASS (compiles and existing non-integration tests pass).

- [ ] **Step 4: Run existing api integration tests for the affected helpers**

Run: `go test -tags integration -p 1 ./internal/api/... -run "TestCreateJobFromSpec|TestValidateJobSpec" -v -timeout 180s`
Expected: PASS - `TestCreateJobFromSpec_CreatesJobAndTasks`, `TestCreateJobFromSpec_PersistsSource`, `TestCreateJobFromSpec_DefaultPriority` all green through the wrapper.

- [ ] **Step 5: Commit**

```bash
git add internal/api/job_spec.go
git commit -m "refactor: delegate api.CreateJobFromSpec to internal/jobcreate"
```

---

## Task 3: Write the failing schedrunner source-persistence integration test (RED)

Add a test that fires a cron schedule whose stored `job_spec` carries a valid Perforce `source` block, then asserts the created task's `source` column round-trips the stream. This MUST fail on current code (cron path drops source) before Task 5 makes it pass.

A new helper `makeSourceSpecJSON` is added next to the existing `makeSpecJSON` so the existing tests keep using `makeSpecJSON` unchanged. The source shape mirrors `internal/api/job_spec_test.go:TestCreateJobFromSpec_PersistsSource`.

**Files:**
- Modify: `internal/schedrunner/runner_test.go` (add one helper after `makeSpecJSON` at line 75, and one test function)

- [ ] **Step 1: Add the source-spec helper**

Insert this helper into `internal/schedrunner/runner_test.go` immediately after the existing `makeSpecJSON` function (after line 75):

```go
func makeSourceSpecJSON(t *testing.T) []byte {
	t.Helper()
	spec, err := json.Marshal(map[string]any{
		"name": "src-job",
		"tasks": []map[string]any{{
			"name":    "t",
			"command": []string{"true"},
			"source": map[string]any{
				"type":   "perforce",
				"stream": "//streams/X/main",
				"sync": []map[string]any{
					{"path": "//streams/X/main/...", "rev": "#head"},
				},
			},
		}},
	})
	require.NoError(t, err)
	return spec
}
```

- [ ] **Step 2: Add the failing test**

Append this test to `internal/schedrunner/runner_test.go`:

```go
func TestRunner_FiresScheduleWithSource_PersistsSource(t *testing.T) {
	h := newRunnerHarness(t)
	ctx := context.Background()
	userID := h.createUser(t, "alice-source@example.com")

	sj, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name:          "nightly-source",
		OwnerID:       userID,
		CronExpr:      "@hourly",
		Timezone:      "UTC",
		JobSpec:       makeSourceSpecJSON(t),
		OverlapPolicy: "skip",
		Enabled:       true,
		NextRunAt:     pgtype.Timestamptz{Time: time.Now().Add(-1 * time.Second), Valid: true},
	})
	require.NoError(t, err)

	runner := schedrunner.NewRunner(h.pool, h.q)
	require.NoError(t, runner.TickOnce(ctx))

	jobs, err := h.q.ListJobsByScheduledJob(ctx, sj.ID)
	require.NoError(t, err)
	require.Len(t, jobs, 1)

	tasks, err := h.q.ListTasksByJob(ctx, jobs[0].ID)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.NotNil(t, tasks[0].Source, "cron-fired task must persist its source spec")
	require.Contains(t, string(tasks[0].Source), `"//streams/X/main"`)
}
```

Note for the implementer: confirm the read-back query name. The api source test reads `tasks[0].Source` from the slice returned by `CreateJobFromSpec`; here we reload from the DB. Use the existing per-job task lister. If `ListTasksByJob` is not the exact generated name, find the correct one with:

Run: `rg -n "func \(q \*Queries\) List.*Task.*Job|TasksByJob" internal/store/`

and substitute it. The struct field is `tasks[0].Source` (the `tasks.source` column, a `[]byte`/json value, same field the api test asserts on).

- [ ] **Step 3: Run the new test and confirm it FAILS (red)**

Run: `go test -tags integration -p 1 ./internal/schedrunner/... -run TestRunner_FiresScheduleWithSource_PersistsSource -v -timeout 180s`
Expected: FAIL. On current code the cron path calls `store.CreateTask` (no source), so `tasks[0].Source` is empty/null and `require.NotNil`/`require.Contains` fails. (If it instead fails to compile, fix the task-lister name per the Step 2 note, then re-run - it must reach the assertion and fail there for a valid red.)

- [ ] **Step 4: Commit the red test**

```bash
git add internal/schedrunner/runner_test.go
git commit -m "test: cron-fired schedule should persist task source (red)"
```

---

## Task 4: Rewire `schedrunner.fireOne` onto `jobcreate.CreateJobFromSpec` and delete the duplicate path (GREEN)

Delete `runnerSpec`, `runnerTaskSpec`, and `Runner.createJob` from `internal/schedrunner/runner.go`. Rewrite `fireOne` to unmarshal `row.JobSpec` into a `jobspec.JobSpec` and call `jobcreate.CreateJobFromSpec`. The legacy `command -> commands` normalization is now handled inside `jobspec.Validate` (called by `jobcreate`), so the hand-rolled normalization is removed with `createJob`.

**Files:**
- Modify: `internal/schedrunner/runner.go` (imports at lines 3-13; `fireOne` at 92-127; delete `runnerSpec`/`runnerTaskSpec` at 72-90 and `createJob` at 129-193)

- [ ] **Step 1: Update imports**

Replace the import block (lines 3-13) of `internal/schedrunner/runner.go` with:

```go
import (
	"context"
	"encoding/json"
	"log"
	"time"

	"relay/internal/jobcreate"
	"relay/internal/jobspec"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)
```

- [ ] **Step 2: Delete `runnerSpec` and `runnerTaskSpec`**

Remove the block at lines 72-90 (the comment `// runnerSpec mirrors ...` through the end of the `runnerTaskSpec` struct). Nothing else should reference these types after Step 4.

- [ ] **Step 3: Rewrite `fireOne` to use the shared path**

Replace the `fireOne` function (lines 92-127) with:

```go
func (r *Runner) fireOne(ctx context.Context, q *store.Queries, row store.ScheduledJob) {
	var spec jobspec.JobSpec
	if err := json.Unmarshal(row.JobSpec, &spec); err != nil {
		log.Printf("schedrunner: schedule %s has invalid job_spec: %v", row.Name, err)
		r.advance(ctx, q, row, pgtype.UUID{}, time.Now().Add(time.Minute))
		return
	}
	sched, err := ParseSchedule(row.CronExpr, row.Timezone)
	if err != nil {
		log.Printf("schedrunner: schedule %s failed to parse cron: %v", row.Name, err)
		r.advance(ctx, q, row, pgtype.UUID{}, time.Now().Add(time.Minute))
		return
	}
	nextFire := sched.Next(time.Now())

	if row.OverlapPolicy == "skip" {
		active, err := q.CountActiveJobsForSchedule(ctx, row.ID)
		if err != nil {
			log.Printf("schedrunner: CountActiveJobsForSchedule for %s: %v", row.Name, err)
			return
		}
		if active > 0 {
			log.Printf("schedrunner: skipping schedule %s (previous run still active)", row.Name)
			r.advance(ctx, q, row, pgtype.UUID{}, nextFire)
			return
		}
	}

	job, _, err := jobcreate.CreateJobFromSpec(ctx, q, spec, row.OwnerID, row.ID)
	if err != nil {
		log.Printf("schedrunner: createJob failed for %s: %v", row.Name, err)
		r.advance(ctx, q, row, pgtype.UUID{}, nextFire)
		return
	}
	r.advance(ctx, q, row, job.ID, nextFire)
}
```

Note: error handling is identical to today (log + `advance` to `nextFire`). The log prefix `createJob failed for %s` is retained verbatim so existing log-scraping/expectations are unchanged. `jobcreate.CreateJobFromSpec` returns `(store.Job, []store.Task, error)`; we take `job.ID` for the advance.

- [ ] **Step 4: Delete `Runner.createJob`**

Remove the entire `createJob` method (originally lines 129-193, the comment `// createJob inserts a job ...` through its closing brace and `return job.ID, nil`). The `advance`, `ReconcileOnStartup` functions below it stay unchanged.

- [ ] **Step 5: Build the schedrunner package**

Run: `go build ./internal/schedrunner/...`
Expected: PASS. If you see "declared and not used" or "imported and not used", a leftover reference to `runnerSpec`/`runnerTaskSpec`/`createJob` remains - search and remove it (none should exist after Steps 2-4).

- [ ] **Step 6: Run the new test and confirm it PASSES (green)**

Run: `go test -tags integration -p 1 ./internal/schedrunner/... -run TestRunner_FiresScheduleWithSource_PersistsSource -v -timeout 180s`
Expected: PASS - cron path now routes through `CreateTaskWithSource`, so `tasks[0].Source` is non-null and contains `"//streams/X/main"`.

- [ ] **Step 7: Run the full schedrunner integration suite (no regressions)**

Run: `go test -tags integration -p 1 ./internal/schedrunner/... -v -timeout 300s`
Expected: PASS - `TestRunner_FiresEligibleSchedule`, `TestRunner_OverlapSkip`, `TestRunner_ReconcileOnStartup_AdvancesPastMissedTriggers`, and the new source test all green. (These use `makeSpecJSON`, whose `command` form is now normalized by `jobspec.Validate` exactly as the deleted hand-rolled code did.)

- [ ] **Step 8: Commit**

```bash
git add internal/schedrunner/runner.go
git commit -m "fix(schedrunner): route cron fires through jobcreate.CreateJobFromSpec so source persists"
```

---

## Task 5: Repo-wide build + test sweep and close the backlog item

Confirm the whole repo builds, no orphaned references to the deleted types remain, and the unit suite is green. Then move the backlog bug to `closed/`.

**Files:**
- Move: `docs/backlog/bug-2026-06-10-cron-jobs-drop-source.md` -> `docs/backlog/closed/bug-2026-06-10-cron-jobs-drop-source.md`

- [ ] **Step 1: Whole-repo build**

Run: `go build ./...`
Expected: PASS.

- [ ] **Step 2: Confirm no dangling references to deleted symbols**

Run: `rg -n "runnerSpec|runnerTaskSpec|\.createJob\(" internal/`
Expected: no matches. (If `rg` exits non-zero with no output, that is the success case.)

- [ ] **Step 3: Run the full unit suite (no Docker)**

Run: `go test ./... -timeout 120s`
Expected: PASS.

- [ ] **Step 4: Move the backlog item to closed**

Run:
```bash
git mv docs/backlog/bug-2026-06-10-cron-jobs-drop-source.md docs/backlog/closed/bug-2026-06-10-cron-jobs-drop-source.md
```

(If `docs/backlog/closed/` does not exist yet, create it first: `mkdir -p docs/backlog/closed` then re-run the `git mv`.)

- [ ] **Step 5: Commit the close**

```bash
git add docs/backlog/closed/bug-2026-06-10-cron-jobs-drop-source.md
git commit -m "backlog: close bug-2026-06-10-cron-jobs-drop-source (cron source persistence fixed)"
```

---

## Self-review (author checklist, completed)

- **Spec coverage:**
  - New `internal/jobcreate` package with moved `CreateJobFromSpec` -> Task 1.
  - api thin wrapper, aliases + `ValidateJobSpec` unchanged -> Task 2.
  - Delete `runnerSpec`/`runnerTaskSpec`/`createJob` incl. legacy normalization; `fireOne` calls `jobcreate.CreateJobFromSpec(ctx, q, spec, row.OwnerID, row.ID)` -> Task 4.
  - Red-first schedrunner source-persistence integration test -> Task 3 (red) confirmed before Task 4 (green).
  - Existing api + schedrunner tests stay green -> Task 2 Step 4, Task 4 Step 7.
  - No migration / no new query / no proto / no `make generate` -> none of the tasks touch `.sql`, `.proto`, or generated files; explicitly excluded.
  - Backlog close via `git mv` -> Task 5 Step 4.
- **Placeholder scan:** every code step shows complete code; the only deferred lookup is the exact generated task-lister name in Task 3, which includes an explicit `rg` command and the field name to use - not a vague "fill in".
- **Type consistency:** `jobcreate.CreateJobFromSpec(ctx, q, spec, submittedBy, scheduledID) (store.Job, []store.Task, error)` is the same signature used by the api wrapper (Task 2) and the schedrunner caller (Task 4, which destructures `job, _, err` and uses `job.ID`). `JobSpec` alias = `jobspec.JobSpec`, so no conversion needed at the api call site.

---

## Relevant file paths

- `internal/jobcreate/jobcreate.go` (new)
- `internal/api/job_spec.go`
- `internal/schedrunner/runner.go`
- `internal/schedrunner/runner_test.go`
- `internal/jobspec/jobspec.go` (read-only reference)
- `internal/api/job_spec_test.go` (regression guard, read-only)
- `docs/backlog/bug-2026-06-10-cron-jobs-drop-source.md` -> `docs/backlog/closed/`
