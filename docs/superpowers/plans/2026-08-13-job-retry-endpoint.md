# POST /v1/jobs/{id}/retry - Operator Re-run of a Terminal Job's Tasks Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship `POST /v1/jobs/{id}/retry?task=failed|all` - one owner-or-admin handler that, inside a single transaction holding the job row lock, returns a `done` or `failed` job's terminal tasks to the queue by bumping `assignment_epoch`, clearing the assignment, resetting `retry_count`, and waking the dispatcher.

**Architecture:** Three new sqlc statements (`RetryJobTasks`, `SelectRetryableTaskIDs` in `tasks.sql`; `GetJobForUpdate` in `jobs.sql`), one new handler plus one extracted authorization helper in `internal/api/jobs.go`, one route in `internal/api/server.go`, and a one-statement change to `handleCancelJob` so both multi-statement writers over `jobs`+`tasks` take the job row lock first. No migration. No new package. No `web/` change.

**Tech Stack:** Go 1.24, PostgreSQL 16, sqlc v1.30.0 (`sql_package: pgx/v5`, `emit_pointers_for_null_types: true`, `emit_sql_as_comment: true`), pgx/v5, golang-migrate, testcontainers-go, testify.

**Spec:** `docs/superpowers/specs/2026-08-13-job-retry-endpoint.md` (approved; do not reopen its decisions except where "Deviations" below records one, with evidence). Line references below of the form `spec:NNN-MMM` are into that file.

**Backlog item:** `docs/backlog/feature-2026-08-13-job-retry-endpoint.md`. Closed in Phase 6 by the conductor with `/backlog close feature-2026-08-13-job-retry-endpoint`, never by hand-editing `status`.

---

## Slice independence declaration

- **BACKEND-ONLY slice. Frontend slice: NONE.** Every file touched is `.sql`, `.go` or `.md`. **Zero files under `web/`.** Do not allocate a `relay-frontend-engineer`. The `git checkout -- web/dist/` rule does not apply; a dirty path under `web/` is a defect in execution.
- **Tasks are strictly SEQUENTIAL. No parallelism is available.** Tasks 3-14 need the sqlc types Task 2 generates; `sqlc generate` rewrites every file in `internal/store/`, so two agents running it in one worktree lose each other's content; Task 7 must precede Task 8 because `handleRetryJob` calls the helper Task 7 extracts; Tasks 8-13 all edit `internal/api/jobs.go` and one shared test file.
- **One `relay-backend-engineer` for the whole plan.**
- **`relay-integration-tester`: Phase 4 only.** The integration tests here **are** the acceptance evidence and are written by the implementing engineer under TDD; deferring them destroys the RED evidence. In Phase 4 point the tester at the two weakest places: the EPQ proof (Task 3) and the cancel/retry interleave (Task 13).

---

## Deviations from the spec, and one spec claim that does not hold

Read before Tasks 3 and 10. These change what two tests can prove, not the design.

1. **Spec Testing item 19 ("force case C by racing two retries") is NOT reachable through the HTTP layer, and decision 9's narrative for it is wrong.** Decision 7 makes `handleRetryJob` take `GetJobForUpdate` first, so two concurrent retries on one job fully serialize. The second's `SelectRetryableTaskIDs` runs after the first commits and sees the reopened tasks as `pending`; and because the first's `RecomputeJobStatus` moved the job to `running`, the second hits the **job status gate** and returns 409 `"job is not finished..."` - not case C, not case A. Decision 9's sentence "If B's mode is a superset of A's ... B ... returns 409 case C" (spec:558-560) describes the UPDATE in isolation, without the lock decision 7 mandates. **This is a finding, reported not designed around.** Case C stays implemented as a defensive branch and is proven **deterministically** in Task 10 with a `BEFORE UPDATE ... RETURN NULL` trigger on `tasks`, following the `installFailDeleteTrigger` precedent (`internal/api/testhelper_test.go:31-42`). That is stronger evidence than a race that cannot occur.
2. **Spec Testing item 2 as literally written is a vacuous RED.** "Reopen a task, then call `RetryJobTasks` again ... proven RED by rewriting the WHERE as `t.id IN (SELECT id FROM selected)`" (spec:877-882) is a *sequential* second call. Under that mutation the second call recomputes `selected` from a fresh snapshot in which the task is already `pending`, so `selected` is empty and the mutated statement also returns zero rows: **the test passes under the mutation**. Task 3 builds the genuine concurrent interleave the property requires.
3. **The spec's `jobOwnerOr404` signature puts `context.Context` second** (spec:235). Go convention is context-first. **Implement the spec's signature verbatim** - the repo runs `go vet` only, which does not check this, and "improving" it is exactly the silent redesign this plan must not do.

---

## Critical files

- `internal/store/query/tasks.sql:95-145` - `IncrementTaskRetryCount`; its forward-reference note at **`:126-133`** is edited in Task 2. `:262-273` `RequeueTaskByID` (the correct analogue). `:12-93` `UpdateTaskStatus` (canonical statement of the terminality and allow-list rules; cross-reference it, do not restate). `:147-157` `GetEligibleTasks` (every dependency must be `done`; **does not consult job status**). `:167-203` `AppendTaskLog` (the `:189-192` note on sqlc needing aliases across CTEs is what you need if Task 2's generate fails). `:208-221` `FailDependentTasks` (walk direction `depends_on_task_id` -> `task_id`). `:223-233` `ClaimTaskForWorker` (requires `pending`). `:298-309` `UpdateTaskStatusEpoch` (TEST-ONLY fixture tool). `:322-333` `CancelJobTasks` (squashes cancellation onto `failed`; its dead `'queued'` literal is pre-existing and **out of scope**).
- `internal/store/query/jobs.sql:89-107` - `RecomputeJobStatus`, cancelled-blind, **not modified**. `:282-292` `JobStatusCounts`, whose comment at `:283-286` is false and is corrected in Task 2.
- `internal/api/jobs.go:676-780` - `handleCancelJob` in full: gate at `:704-715` (extracted in Task 7), `GetJob` at `:694` (becomes `GetJobForUpdate`), post-commit publish at `:773-777`. `:39-113` - `jobResponse`, `toJobResponse`.
- `internal/api/workers.go:34-40` - `disableWorkerResponse`, the embed-plus-one-key shape. `:483-495` - requeue then `NotifyTaskSubmitted` **inside** the tx then commit. `:543-556` - the explicit row-count gate on notify.
- `internal/api/server.go:105-122` - Jobs route block; the sentence at `:110` becomes false when the route lands. `:186-215` - `writeJSON`/`writeError`/`readJSON` (**never called here**). `:219-237` - `uuidStr`, `parseUUID`.
- `internal/worker/handler.go:539-575` - the agent retry branch. `terminal && task.RetryCount < task.Retries` at `:550` is `retry_count`'s **only** behavioral consumer.
- Test helpers to reuse, never redefine: `internal/store/testhelper_test.go:20-84` (`newTestQueries`, `newTestUser`, `newTestWorker`); `internal/store/updatetaskstatusepoch_guard_test.go` (**model for Task 1**, and its `repoRoot(t)` at `:78-94` is in scope for Task 1's untagged file); `internal/store/tasks_status_vocabulary_lockstep_test.go:21-52`; `internal/api/testhelper_test.go:31-42,72-98`; `internal/api/api_test.go:27-61` (`newTestServer`, `createTestUser`, `createTestToken`); `internal/api/jobs_cancel_test.go` **in full** (`newCancelTestServer` `:58-91`, `parseJobUUID` `:238-243`; every assertion must stay byte-identical); `internal/api/users_integration_test.go:314` (`uuidString`); `internal/scheduler/notify_test.go:17-56` (LISTEN shape).
- `README.md:1193-1201` - Jobs REST table. `CLAUDE.md` Invariants - read in full before Task 2.

### Invariants: which apply

**Do NOT apply** (stated so nobody re-derives them): **End the generation before releasing the resource** (no async lifecycle; the DB analogue - bump the epoch in the same statement that reopens the row - is honored). **Single job-spec pipeline** (no spec parsed; this endpoint reuses existing task rows and must never grow a path that creates new ones). **One bounded sender per gRPC stream** (no gRPC; unlike cancel, retry sends **no** agent signal - every reopened row was terminal, so `sendCancelSignals` has no analogue). **Identity-checked teardown** (no connection state). **No interior pointers across locks** (no registry touched). **tokenhash** (nothing is hashed).

**Do apply:** (1) **Epoch fence** - `RetryJobTasks` takes the "conditionally end the assignment" branch: the bump sits in the same UPDATE as the status allow-list, so it only accompanies a generation actually being ended. Proof: Tasks 3, 4. (2) **Allow-lists, not deny-lists** - both new `tasks.status` predicates are allow-lists; the single negation (`dep.status <> 'pending'`) is on the **blocking** side, where negation is the fail-closed direction. (3) **Gate side effects on the fence having matched** - notify, SSE publish and the 200 all require `len(reopened) == len(selected) >= 1`, and the notify is inside the tx so a rollback un-sends it. Proof: Task 11. (4) **Single JSON entry point** - no body; `readJSON` is not called and **must not be added**. (5) **Authorization resolved server-side** from `AuthUser`.

---

## Conventions and gotchas

1. **SQL is the source of truth.** Edit `internal/store/query/*.sql`, then regenerate. **Never hand-edit `internal/store/*.sql.go` or `models.go`.**
2. **`make` is NOT on PATH.** Raw invocations: `sqlc generate`; `go test ./... -timeout 120s`; `go test -tags integration -p 1 ./... -timeout 900s`; `go vet -tags integration ./...`. **Run `sqlc generate` alone** - no `.proto` change here, and `buf generate` would churn `internal/proto/` for nothing.
3. **The sqlc CRLF hazard.** sqlc emits LF; this repo is CRLF, so `sqlc generate` rewrites line endings across **every** emitted file. After generating: `git status --short internal/store/`, then `git diff --ignore-all-space`. **Only `tasks.sql.go` and `jobs.sql.go` may show content changes.** For every other listed file, if `git diff --ignore-all-space <file>` prints nothing it is pure churn - `git checkout -- <file>`. `models.go` is expected in that set (no column is added). **Never `git checkout -- internal/store/` as a directory.** **Known trap, twice recorded in this project's retros: the revert can silently discard the regenerated file, leaving a doc comment contradicting its own SQL - which still compiles.** Task 2 Step 6 is a mandatory read-back; do not skip it because the build is green.
4. **Integration tests are the gate.** Every behavioral test here is `//go:build integration` and needs Docker Desktop (this machine uses `desktop-linux` automatically). `-p 1` is mandatory. The one exception is Task 1's guard, deliberately untagged.
5. **`go vet -tags integration ./...`** is the compile gate for tagged code; `go build ./...` does not compile it. Run after every task touching a test file.
6. **Hard gate: no existing test may have an assertion changed.** This plan edits one existing test file and only its doc comment (Task 2, step 3e). If any existing test goes red - especially in `internal/api/jobs_cancel_test.go` - **STOP and report it as a finding.** An assertion needing adjustment after Task 7 is evidence the `GetJobForUpdate` swap changed behavior, which it must not.
7. **Every RED proof must be observed and recorded.** Each names the exact mutation, command, and the **single** test expected to redden. A mutation that reddens many tests through shared setup is coupling, not strength - say so if you see it. If a stated RED does not reproduce, **STOP and report**; never weaken the test until it does.
8. **Commit at each task boundary. A mutation is never committed** - revert it in the step that observed the failure.
9. **No em dashes or en dashes** anywhere, including SQL comments and commit messages.
10. **Do not touch `RecomputeJobStatus`** (spec decision 4c).

---

## Test-to-acceptance-criteria map

| Spec AC | Test | Task |
| --- | --- | --- |
| 1 route, `auth(...)`, owner-or-admin, 404 on deny | `TestRetryJob_Unauthenticated_401`, `TestRetryJob_NonOwner_404_NoSideEffects`, `TestRetryJob_Owner_200`, `TestRetryJob_Admin_200_OnAnotherUsersJob` | 8, 9 |
| 1 `failed` vs `all` differ | `TestRetryJob_FailedVersusAll_SelectDifferentSets` | 9 |
| 2 `task` required and exact, no write | `TestRetryJob_TaskParam_Rejects` (5 subtests), `TestRetryJob_TaskParam_400_BeforeAnyDatabaseWork` | 8 |
| 3a RED without the row-level allow-list | `TestRetryJobTasks_StatusAllowList_DoneTaskIsNotReopenedByFailedMode` | 2 |
| 3b RED with the allow-list moved into the CTE | `TestRetryJobTasks_RowLevelPredicate_ConcurrentSecondRetryDoesNotDoubleBumpEpoch` | 3 |
| 4a epoch +1 exactly, per row | `TestRetryJobTasks_ReopenedRowFields_EpochIncrementsByExactlyOne` | 4 |
| 4b status / log / retry from the dead generation dropped | `TestRetryJobTasks_PreviousGenerationIsDead_StatusLogAndRetryAllRejected` | 4 |
| 5 `retry_count` reset restores agent budget | `TestRetryJob_RetryCountResetRestoresAgentRetryBudget` | 12 |
| 6 cancelled job refused, nothing changed | `TestRetryJob_CancelledJob_409_NothingChanged` | 10 |
| 7 dependents that ran block; cascade-failed do not | `TestRetryJobTasks_DependentsGuard_AlreadyRanDependentBlocks`, `..._CascadeFailedDependentDoesNotBlock` | 6 |
| 8 all-or-nothing, no partial commit | `TestRetryJobTasks_DependentsGuard_ExclusionIsAllOrNothing`, `TestRetryJob_PartialMatch_409_CaseC_RollbackIsTotal` | 6, 10 |
| 9 `IncrementTaskRetryCount` uncalled, structurally | `TestIncrementTaskRetryCountHasNoCallerOutsideTheAgentPath` (untagged) | 1 |
| 10 notify fires exactly on success | `TestRetryJob_NotifyTaskSubmitted_FiresOnSuccessOnly` (4 subtests) | 11 |
| 11 `GetJobForUpdate` in both handlers; cancel tests byte-identical | `TestGetJobForUpdate_TakesARowLockThatBlocksASecondReader`, `TestRetryJob_CancelSerialization_NeverCancelledJobWithPendingTasks`, Task 7 gate | 5, 7, 13 |
| 12 jobs-stats accepted in writing; comment corrected | Task 2 Steps 3d, 6 | 2 |
| 13 lockstep doc comment names both statements | Task 2 Step 3e | 2 |
| 14 README | Task 14 | 14 |
| 15 gates green, generate verified | Task 14 | 14 |
| 16 no file outside the table modified; `web/` untouched | Task 14 Step 3 | 14 |
| 17 frontend item drops its caveat | Phase 6 proposal, **not applied here** | - |
| spec test 5 `all` widens no further | `TestRetryJobTasks_AllMode_WidensToTerminalSetAndNoFurther` | 5 |
| spec test 8 transitive guard | `TestRetryJobTasks_DependentsGuard_TransitiveDescendantBlocks` | 6 |
| spec test 15 zero matched -> 409 case A | `TestRetryJob_ZeroMatched_409_CaseA` | 10 |
| spec test 17 recomputed inside the tx | `TestRetryJob_JobStatusRecomputedToRunningInsideTheTransaction` | 9 |
| spec test 22 response is `jobResponse` plus one key | `TestRetryJob_ResponseShape_JobPlusTasksRetried` | 9 |

---

## Task 1: Structural guard - `IncrementTaskRetryCount` has no caller outside the agent path

Lands first: it is the backstop against the cheapest failure mode the backlog item names. Deliberately **untagged**, so it runs in the plain gate with no Docker. Today the only two `.go` files under `internal/` containing the identifier are `internal/worker/handler.go:551` and the generated `internal/store/tasks.sql.go`. Modeled on `updatetaskstatusepoch_guard_test.go` and **reusing its `repoRoot(t)`** (same package `store_test`, also untagged). Do not define a second `repoRoot`.

**Files:** Create `internal/store/incrementtaskretrycount_guard_test.go`

- [ ] **Step 1: Write the failing test**

```go
package store_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIncrementTaskRetryCountHasNoCallerOutsideTheAgentPath is a STRUCTURAL
// guard, deliberately NOT integration-tagged so it runs in the plain
// `go test ./...` gate on every change.
//
// IncrementTaskRetryCount (query/tasks.sql) is the AGENT-DRIVEN retry. Its three
// predicates - assignment_epoch, worker_id, and
// status IN ('pending','dispatched','running') - are the exact inverse of an
// operator re-run's preconditions: POST /v1/jobs/{id}/retry reopens tasks that
// ARE terminal and has no worker identity to supply, so both the status and the
// worker predicate would reject every call it made. The symptom of that mistake
// is not a crash: it is "the endpoint silently does nothing", which a test
// asserting only a 200 would not catch.
//
// The operator path has its own statement, RetryJobTasks. This test keeps the
// two apart: sqlc re-exports IncrementTaskRetryCount on *store.Queries on every
// regeneration, so it is permanently one autocomplete away from the wrong site.
//
// If this goes RED, do not add an exception. Either the caller wants
// RetryJobTasks (the operator analogue of RequeueTaskByID), or it genuinely
// drives the agent-side retry budget and belongs in internal/worker/handler.go.
//
// Known weakness, accepted: a rename defeats it. Same weakness and same trade as
// TestUpdateTaskStatusEpochHasNoProductionCaller.
func TestIncrementTaskRetryCountHasNoCallerOutsideTheAgentPath(t *testing.T) {
	root := repoRoot(t)
	const ident = "IncrementTaskRetryCount"

	// The generated store layer necessarily defines it; the agent status path is
	// its one legitimate caller.
	allowed := map[string]bool{
		filepath.Join(root, "internal", "store", "tasks.sql.go"): true,
		filepath.Join(root, "internal", "worker", "handler.go"):  true,
	}

	var offenders []string
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if allowed[path] {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), ident) {
			rel, _ := filepath.Rel(root, path)
			offenders = append(offenders, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking internal/: %v", err)
	}

	if len(offenders) > 0 {
		t.Fatalf("%s is the AGENT-DRIVEN retry and must be called only from "+
			"internal/worker/handler.go, but it appears in: %v\n"+
			"An operator re-run (POST /v1/jobs/{id}/retry) must use RetryJobTasks: every "+
			"predicate on %s would reject it. See the note on the statement in "+
			"internal/store/query/tasks.sql.", ident, offenders, ident)
	}
}
```

- [ ] **Step 2: Run it - PASS on a clean tree**

Run: `go test ./internal/store/... -run TestIncrementTaskRetryCountHasNoCallerOutsideTheAgentPath -v -timeout 60s`
Expected: PASS. By itself this is **no evidence** that the walker reaches anything; Step 3 supplies that.

- [ ] **Step 3: Prove it RED against the real exposure**

Mutation - add one line inside `handleCancelJob` in `internal/api/jobs.go`, immediately after `ctx := r.Context()` (line 677):

```go
	_ = "IncrementTaskRetryCount" // MUTATION - revert
```

Run: `go test ./internal/store/... -run TestIncrementTaskRetryCountHasNoCallerOutsideTheAgentPath -v -timeout 60s`
Expected: **FAIL**, with `internal/api/jobs.go` in the offenders list. That is the discriminating evidence: the walker descends into `internal/api`, where Task 8 adds the handler.

Revert and re-run:

```
git checkout -- internal/api/jobs.go
go test ./internal/store/... -run TestIncrementTaskRetryCountHasNoCallerOutsideTheAgentPath -v -timeout 60s
```
Expected: PASS. Record both observations in the PR.

- [ ] **Step 4: Whole untagged gate**

Run: `go test ./... -timeout 120s` then `go vet -tags integration ./...`
Expected: PASS, clean.

- [ ] **Step 5: Commit**

```bash
git add internal/store/incrementtaskretrycount_guard_test.go
git commit -m "test(store): guard IncrementTaskRetryCount against callers outside the agent path

The operator re-run endpoint must not call the agent-driven retry statement: its
epoch, worker and status predicates are the exact inverse of an operator's
preconditions, so every call would silently affect zero rows. Untagged so it
runs in the plain gate. Proven RED by planting the identifier in internal/api."
```

---

## Task 2: The three store statements, two comment corrections, and the allow-list RED proof

The only task that runs `sqlc generate`. Ends with a mandatory read-back of both regenerated files.

**Files:**
- Modify: `internal/store/query/tasks.sql` (append two statements; replace the comment at `:126-133`)
- Modify: `internal/store/query/jobs.sql` (append one statement; replace the comment at `:283-286`)
- Modify: `internal/store/tasks_status_vocabulary_lockstep_test.go` (doc comment only, after `:47`)
- Regenerated: `internal/store/tasks.sql.go`, `internal/store/jobs.sql.go`
- Create: `internal/store/retry_job_tasks_integration_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/store/retry_job_tasks_integration_test.go`:

```go
//go:build integration

package store_test

import (
	"context"
	"testing"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

// retryFixture is one job plus one worker. Its helpers put tasks into a status
// the way production does - claim (bumping assignment_epoch to 1 and setting
// worker_id), then write through the fenced UpdateTaskStatus - so a planted task
// carries a real assignee and a real epoch. That is what makes the "previous
// generation is dead" assertions in Task 4 mean anything.
type retryFixture struct {
	q   *store.Queries
	ctx context.Context
	job store.Job
	w   store.Worker
}

func newRetryFixture(t *testing.T) *retryFixture {
	t.Helper()
	q := newTestQueries(t)
	ctx := context.Background()
	user := newTestUser(t, q, false)
	w := newTestWorker(t, q)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "retry-job", Priority: "normal", SubmittedBy: user.ID, Labels: []byte("{}"),
	})
	require.NoError(t, err)
	return &retryFixture{q: q, ctx: ctx, job: job, w: w}
}

// pending creates a task and leaves it at epoch 0 with worker_id NULL.
func (f *retryFixture) pending(t *testing.T, name string) store.Task {
	t.Helper()
	task, err := f.q.CreateTask(f.ctx, store.CreateTaskParams{
		JobID: f.job.ID, Name: name, Commands: []byte(`[["echo","x"]]`),
		Env: []byte("{}"), Requires: []byte("{}"), Retries: 0,
	})
	require.NoError(t, err)
	return task
}

// dispatched creates a task and claims it: status 'dispatched', epoch 1.
func (f *retryFixture) dispatched(t *testing.T, name string) store.Task {
	t.Helper()
	claimed, err := f.q.ClaimTaskForWorker(f.ctx, store.ClaimTaskForWorkerParams{
		ID: f.pending(t, name).ID, WorkerID: f.w.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)
	return claimed
}

// inStatus drives a task to status through the production path. Valid for
// 'running', 'done', 'failed', 'timed_out'. Ends at epoch 1 with worker_id set.
func (f *retryFixture) inStatus(t *testing.T, name, status string) store.Task {
	t.Helper()
	claimed := f.dispatched(t, name)
	updated, err := f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
		ID: claimed.ID, Status: status, WorkerID: claimed.WorkerID,
		AssignmentEpoch: claimed.AssignmentEpoch,
	})
	require.NoError(t, err)
	require.Equal(t, status, updated.Status)
	return updated
}

func (f *retryFixture) get(t *testing.T, id pgtype.UUID) store.Task {
	t.Helper()
	got, err := f.q.GetTask(f.ctx, id)
	require.NoError(t, err)
	return got
}

func (f *retryFixture) dep(t *testing.T, task, dependsOn store.Task) {
	t.Helper()
	require.NoError(t, f.q.CreateTaskDependency(f.ctx, store.CreateTaskDependencyParams{
		TaskID: task.ID, DependsOnTaskID: dependsOn.ID,
	}))
}

// TestRetryJobTasks_StatusAllowList_DoneTaskIsNotReopenedByFailedMode is the
// item's criterion "proven RED against a version without the status allow-list:
// a done task must not be reopened by ?task=failed".
//
// The assertion set is wider than "status is still done" on purpose: a mutation
// dropping the allow-list would also clear the done task's assignee, wipe its
// finished_at and bump its epoch - which is what would make a trailing log chunk
// from its own successful run vanish.
func TestRetryJobTasks_StatusAllowList_DoneTaskIsNotReopenedByFailedMode(t *testing.T) {
	f := newRetryFixture(t)

	doneTask := f.inStatus(t, "t-done", "done")
	failedTask := f.inStatus(t, "t-failed", "failed")

	reopened, err := f.q.RetryJobTasks(f.ctx, store.RetryJobTasksParams{
		JobID: f.job.ID, IncludeDone: false,
	})
	require.NoError(t, err)
	require.Len(t, reopened, 1, "task=failed must reopen exactly the failed task")
	require.Equal(t, failedTask.ID, reopened[0])

	gotDone := f.get(t, doneTask.ID)
	require.Equal(t, "done", gotDone.Status, "a done task must not be reopened by task=failed")
	require.Equal(t, doneTask.AssignmentEpoch, gotDone.AssignmentEpoch,
		"an unmatched row must not have its generation ended")
	require.True(t, gotDone.WorkerID.Valid, "an unmatched row must keep its assignee")
	require.True(t, gotDone.FinishedAt.Valid, "an unmatched row must keep finished_at")

	require.Equal(t, "pending", f.get(t, failedTask.ID).Status)
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test -tags integration -p 1 ./internal/store/... -run TestRetryJobTasks_StatusAllowList -v -timeout 300s`
Expected: **compile failure** - `f.q.RetryJobTasks undefined`, `undefined: store.RetryJobTasksParams`. This is a **weak RED**: it proves absence, not correctness. The discriminating RED is Step 8.

- [ ] **Step 3: Write the SQL**

**3a.** In `internal/store/query/tasks.sql`, replace the forward-reference paragraph at `:126-133` (the block ending `-- docs/backlog/feature-2026-06-26-web-enabler-backend-endpoints.md.`) with:

```sql
-- This statement is for the AGENT-DRIVEN retry only, and its preconditions are
-- the exact opposite of an operator re-run. POST /v1/jobs/{id}/retry must NOT
-- call it: that endpoint reopens tasks that ARE terminal and has no worker
-- identity to supply, so both the status and the worker predicate would reject
-- every call, and the symptom would be an endpoint that silently does nothing.
-- It has its own statement, RetryJobTasks (below in this file), with an explicit
-- `status IN ('failed','timed_out')` allow-list and its own epoch bump - the
-- operator analogue of RequeueTaskByID, not of this. The separation is enforced
-- by TestIncrementTaskRetryCountHasNoCallerOutsideTheAgentPath, which fails if
-- this identifier appears in any non-test Go file outside
-- internal/worker/handler.go.
```

**3b.** Append `SelectRetryableTaskIDs` and `RetryJobTasks` to the end of `internal/store/query/tasks.sql`. **Copy the comment blocks verbatim from the spec** - `spec:701-710` for the first and `spec:604-650` for the second; those comments are part of the deliverable and are how the last three fence bugs stayed fixed. Add to `RetryJobTasks`'s block one extra line naming the pin, immediately after the `...double-bumps assignment_epoch.` sentence:

```sql
-- This is pinned by
-- TestRetryJobTasks_RowLevelPredicate_ConcurrentSecondRetryDoesNotDoubleBumpEpoch.
```

and add to `SelectRetryableTaskIDs`'s block a final line: `-- TestTasksStatusVocabularyIsExactly names this statement.`

The statement bodies, exactly:

```sql
-- name: SelectRetryableTaskIDs :many
SELECT id FROM tasks
WHERE job_id = sqlc.arg(job_id)
  AND (status IN ('failed','timed_out')
       OR (sqlc.arg(include_done)::bool AND status = 'done'))
ORDER BY created_at;
```

```sql
-- name: RetryJobTasks :many
WITH RECURSIVE selected AS (
    SELECT id FROM tasks
    WHERE job_id = sqlc.arg(job_id)
      AND (status IN ('failed','timed_out')
           OR (sqlc.arg(include_done)::bool AND status = 'done'))
), descendants AS (
    SELECT td.task_id AS id
    FROM task_dependencies td
    WHERE td.depends_on_task_id IN (SELECT id FROM selected)
  UNION
    SELECT td.task_id
    FROM task_dependencies td
    JOIN descendants dd ON dd.id = td.depends_on_task_id
)
UPDATE tasks t
SET status           = 'pending',
    worker_id        = NULL,
    started_at       = NULL,
    finished_at      = NULL,
    retry_count      = 0,
    assignment_epoch = t.assignment_epoch + 1
WHERE t.job_id = sqlc.arg(job_id)
  AND (t.status IN ('failed','timed_out')
       OR (sqlc.arg(include_done)::bool AND t.status = 'done'))
  AND NOT EXISTS (
        SELECT 1
        FROM descendants d
        JOIN tasks dep ON dep.id = d.id
        WHERE dep.status <> 'pending'
          AND d.id NOT IN (SELECT id FROM selected)
      )
RETURNING t.id;
```

`retry_count = 0` is spec decision 3 and the comment block says so: its one behavioral consumer is `terminal && task.RetryCount < task.Retries` at `internal/worker/handler.go:550`, so leaving it exhausted hands the operator a re-run with zero agent retries - exactly what they pressed Retry to escape.

**3c.** Append to the end of `internal/store/query/jobs.sql`, comment block copied verbatim from `spec:721-735`, plus a final line `-- Do not "optimize" either handler back to GetJob.`:

```sql
-- name: GetJobForUpdate :one
SELECT * FROM jobs WHERE id = $1 FOR UPDATE;
```

**3d.** In `internal/store/query/jobs.sql`, replace the four comment lines at `:283-286` with:

```sql
-- Fleet-wide job counts for the dashboard KPI strip. running/queued are current
-- totals; done_24h/failed_24h are windowed on updated_at as a finish-time proxy.
-- updated_at has TWO writers, not one. An earlier version of this comment
-- claimed "the only writer of updated_at is UpdateJobStatus"; that was already
-- false when written, because RecomputeJobStatus also stamps NOW()
-- unconditionally on every call, after every task status transition. So
-- updated_at means "time of the last task-level event", not "time of the last
-- job-status transition".
-- The proxy still holds, on this narrower invariant: a job only HAS status
-- 'done' or 'failed' when its last task event was the one that finished it, and
-- a terminal task is unwritable (UpdateTaskStatus and IncrementTaskRetryCount
-- both carry `status IN ('pending','dispatched','running')`), so no later task
-- event can move updated_at while the job sits in a terminal bucket.
-- POST /v1/jobs/{id}/retry does not falsify it either: a retried job leaves both
-- buckets the instant it becomes 'running', and re-enters the appropriate bucket
-- when it finishes again with an updated_at equal to that new finish. The only
-- effect is a transient undercount while it re-runs, which is defensible on the
-- merits and self-corrects. Accepted in writing - see decision 8 of
-- docs/superpowers/specs/2026-08-13-job-retry-endpoint.md.
-- docs/backlog/bug-2026-06-05-jobs-stats-24h-updated-at-proxy.md stays OPEN; its
-- predicted trigger condition did not fire.
```

**3e.** In `internal/store/tasks_status_vocabulary_lockstep_test.go`, insert two bullets into the doc comment immediately after the `RecomputeJobStatus` bullet ending at `:47`:

```go
//   - RetryJobTasks (query/tasks.sql) - `status IN ('failed','timed_out')`, plus
//     'done' when include_done is true. This is the OPERATOR re-run's selection
//     (POST /v1/jobs/{id}/retry). A new TERMINAL status probably belongs in
//     ?task=all's widening, and belongs in ?task=failed's only if it is a
//     failure mode the way timed_out is. A new NON-TERMINAL status must stay out
//     of BOTH modes: this statement clears worker_id and bumps the epoch, so
//     admitting a non-terminal status would let an operator retry evict a live
//     agent. The same statement's dependents guard reads
//     `dep.status <> 'pending'` - a negation, because there the predicate
//     authorizes BLOCKING, so a new status must block. Do not "fix" it into an
//     allow-list.
//   - SelectRetryableTaskIDs (query/tasks.sql) - the unguarded twin of that
//     selection, used only to classify the endpoint's three 409s. Its status
//     predicate must stay byte-identical to RetryJobTasks's; change both or
//     neither.
```

- [ ] **Step 4: Regenerate**

Run: `sqlc generate`
Expected: exit 0, no output.
**If it fails with `column reference "id" is ambiguous`**, add explicit aliases and fully qualify columns - the same analyzer limitation documented at `AppendTaskLog` (`tasks.sql:189-192`). **Do not restructure the predicate to make the error go away**, and in particular do not move the status test into the CTE: that is the exact mutation Task 3 exists to catch.

- [ ] **Step 5: CRLF cleanup**

```
git status --short internal/store/
git diff --ignore-all-space
```
Only `internal/store/tasks.sql.go` and `internal/store/jobs.sql.go` may show content. For each other listed file, run `git diff --ignore-all-space internal/store/<file>`; if it prints nothing, `git checkout -- internal/store/<file>`. `models.go` is expected in that set. Never checkout the directory.

- [ ] **Step 6: Read back both regenerated files (MANDATORY)**

This step exists because the checkout dance has twice discarded a regenerated file here, leaving a doc comment contradicting its own source - which compiles.

Confirm in `internal/store/tasks.sql.go`: `func (q *Queries) RetryJobTasks(ctx context.Context, arg RetryJobTasksParams) ([]pgtype.UUID, error)` with `RetryJobTasksParams{JobID pgtype.UUID; IncludeDone bool}`; `func (q *Queries) SelectRetryableTaskIDs(ctx context.Context, arg SelectRetryableTaskIDsParams) ([]pgtype.UUID, error)`. Confirm in `internal/store/jobs.sql.go`: `func (q *Queries) GetJobForUpdate(ctx context.Context, id pgtype.UUID) (Job, error)`.

Then the mechanical string check:

```
git grep -c "It has its own statement, RetryJobTasks" -- internal/store/tasks.sql.go
git grep -c "THE STATUS ALLOW-LIST MUST STAY IN THIS WHERE CLAUSE" -- internal/store/tasks.sql.go
git grep -c "feature-2026-06-26-web-enabler-backend-endpoints" -- internal/store/tasks.sql.go
git grep -c "updated_at has TWO writers" -- internal/store/jobs.sql.go
git grep -c "the only writer of updated_at is UpdateJobStatus" -- internal/store/jobs.sql.go
```
Expected: `1`, `1`, no match (exit 1), `1`, no match. Any deviation means the revert ate the regeneration - re-run `sqlc generate` and redo Step 5 more carefully.

- [ ] **Step 7: Run the test to verify it passes**

Run: `go test -tags integration -p 1 ./internal/store/... -run TestRetryJobTasks_StatusAllowList -v -timeout 300s`
Expected: PASS.

- [ ] **Step 8: Prove it RED (item acceptance criterion 2)**

Mutation: in `internal/store/query/tasks.sql`, delete **only** these two lines from `RetryJobTasks`'s row-level `WHERE`:

```sql
  AND (t.status IN ('failed','timed_out')
       OR (sqlc.arg(include_done)::bool AND t.status = 'done'))
```

Leave the `selected` CTE untouched, so `sqlc.arg(include_done)` stays referenced and the params struct is unchanged. Then run `sqlc generate` and:

Run: `go test -tags integration -p 1 ./internal/store/... -run TestRetryJobTasks_StatusAllowList -v -timeout 300s`
Expected: **FAIL** on `a done task must not be reopened by task=failed` (and on `require.Len(t, reopened, 1)`). Exactly one test reddens - it is the only one that exists yet. Record the output in the PR.

Revert: `git checkout -- internal/store/query/tasks.sql`, `sqlc generate`, repeat Step 5 and Step 6, re-run Step 7 (PASS).

- [ ] **Step 9: Commit**

```bash
git add internal/store/query/tasks.sql internal/store/query/jobs.sql \
        internal/store/tasks.sql.go internal/store/jobs.sql.go \
        internal/store/tasks_status_vocabulary_lockstep_test.go \
        internal/store/retry_job_tasks_integration_test.go
git commit -m "feat(store): RetryJobTasks, SelectRetryableTaskIDs and GetJobForUpdate

The operator re-run's own statement: a terminal-status allow-list in the
UPDATE's own row-level WHERE, an epoch bump conditioned on the row having
matched, worker_id cleared, retry_count reset, and an all-or-nothing dependents
guard that does not block on a dependent reopened by the same request.

Also corrects two comments: IncrementTaskRetryCount's forward reference now
names RetryJobTasks, and JobStatusCounts no longer claims UpdateJobStatus is the
only writer of updated_at. Proven RED by deleting the row-level status conjunct."
```

---

## Task 3: The row-level predicate is where concurrency is decided (spec decision 9)

The single most likely way to ship this endpoint subtly broken. Read Deviation 2 first: the spec's sequential version of this test passes under the mutation, so the test must build a genuine concurrent interleave. Synchronization is by observing the blocked backend in `pg_stat_activity`, not by sleeping.

**Files:** Modify `internal/store/retry_job_tasks_integration_test.go`

- [ ] **Step 1: Write the failing test**

Append (and add `"github.com/jackc/pgx/v5/pgxpool"`, `"sync"`, `"time"` to the imports; `newRetryFixture` needs the pool, so add a `pool` field - change `newTestQueries(t)` to `pool := newTestPool(t); q := store.New(pool)` and store both):

```go
// TestRetryJobTasks_RowLevelPredicate_ConcurrentSecondRetryDoesNotDoubleBumpEpoch
// pins spec decision 9: the status allow-list must live in the UPDATE's own
// row-level WHERE, not only in the `selected` CTE.
//
// Under READ COMMITTED a blocked UPDATE re-evaluates its ROW-LEVEL qual against
// the updated tuple (EvalPlanQual); it does not re-execute CTEs, which were
// materialized from the statement's original snapshot. So the interleave has to
// be real: B's statement must START (materializing `selected` from a snapshot in
// which the tasks are still terminal) and then BLOCK on A's row locks, and A
// must commit while B is blocked.
//
// A sequential second call proves nothing here: it recomputes `selected` from a
// fresh snapshot in which the task is already `pending`, so the mutated
// statement also returns zero rows.
func TestRetryJobTasks_RowLevelPredicate_ConcurrentSecondRetryDoesNotDoubleBumpEpoch(t *testing.T) {
	f := newRetryFixture(t)
	task := f.inStatus(t, "t-failed", "failed")
	require.Equal(t, int32(1), task.AssignmentEpoch)

	args := store.RetryJobTasksParams{JobID: f.job.ID, IncludeDone: false}

	txA, err := f.pool.Begin(f.ctx)
	require.NoError(t, err)
	defer txA.Rollback(f.ctx)
	idsA, err := f.q.WithTx(txA).RetryJobTasks(f.ctx, args)
	require.NoError(t, err)
	require.Len(t, idsA, 1, "A must reopen the failed task")

	var (
		wg     sync.WaitGroup
		idsB   []pgtype.UUID
		errB   error
		txB    pgx.Tx
	)
	txB, err = f.pool.Begin(f.ctx)
	require.NoError(t, err)
	defer txB.Rollback(f.ctx)

	wg.Add(1)
	go func() {
		defer wg.Done()
		idsB, errB = f.q.WithTx(txB).RetryJobTasks(f.ctx, args)
	}()

	// Wait until B is genuinely blocked on a row lock. This replaces a sleep:
	// if B has not reached the lock, committing A would let B run against a
	// post-A snapshot and the test would prove nothing.
	require.Eventually(t, func() bool {
		var n int
		require.NoError(t, f.pool.QueryRow(f.ctx,
			`SELECT count(*) FROM pg_stat_activity
			  WHERE datname = current_database()
			    AND wait_event_type = 'Lock' AND state = 'active'`).Scan(&n))
		return n > 0
	}, 10*time.Second, 50*time.Millisecond, "B never blocked on A's row lock")

	require.NoError(t, txA.Commit(f.ctx))
	wg.Wait()
	require.NoError(t, errB)
	require.NoError(t, txB.Commit(f.ctx))

	require.Empty(t, idsB,
		"B must reopen nothing: EvalPlanQual re-checks the row-level status "+
			"predicate against the updated tuple, which is now 'pending'")

	got := f.get(t, task.ID)
	require.Equal(t, "pending", got.Status)
	require.Equal(t, int32(2), got.AssignmentEpoch,
		"exactly ONE epoch bump may result from two concurrent retries; a second "+
			"bump would end a generation another operator's retry may already be running")
}
```

- [ ] **Step 2: Run it to verify it passes against the correct statement**

Run: `go test -tags integration -p 1 ./internal/store/... -run TestRetryJobTasks_RowLevelPredicate -v -timeout 300s`
Expected: PASS. (Add `"github.com/jackc/pgx/v5"` to imports for `pgx.Tx`.)

- [ ] **Step 3: Prove it RED against the tidier spelling that would ship broken**

Mutation - in `internal/store/query/tasks.sql`, replace `RetryJobTasks`'s two row-level status lines with membership only:

```sql
  AND t.id IN (SELECT id FROM selected)
```

(delete the `AND (t.status IN ('failed','timed_out') OR (sqlc.arg(include_done)::bool AND t.status = 'done'))` conjunct; the CTE keeps the arg referenced). Run `sqlc generate`, then:

Run: `go test -tags integration -p 1 ./internal/store/... -run TestRetryJobTasks_RowLevelPredicate -v -timeout 300s`
Expected: **FAIL** on `exactly ONE epoch bump ...` with `AssignmentEpoch == 3`, and on `B must reopen nothing`. Exactly one test reddens (`TestRetryJobTasks_StatusAllowList` still passes, because the CTE still carries the status test - which is precisely why this second test is needed).

**If the mutation does NOT redden this test, STOP and report it as a finding.** Do not weaken the test and do not delete the statement's EPQ comment. The plausible cause is a plan shape in which the CTE membership becomes a semi-join rather than a re-checkable SubPlan; capture `EXPLAIN (ANALYZE, VERBOSE)` for the mutated statement and hand it to the conductor. The comment's claim and the code must agree, and wrong prose about correct code is this project's most repeated defect.

Revert: `git checkout -- internal/store/query/tasks.sql`, `sqlc generate`, Task 2 Step 5 cleanup, Task 2 Step 6 read-back, re-run Step 2 (PASS).

- [ ] **Step 4: Commit**

```bash
git add internal/store/retry_job_tasks_integration_test.go
git commit -m "test(store): pin RetryJobTasks's row-level status predicate under concurrency

Two concurrent retries must produce exactly one epoch bump. Proven RED against
the tidier 'WHERE t.id IN (SELECT id FROM selected)' spelling, which moves the
status test out of the row-level qual so EvalPlanQual cannot re-check it. The
spec's sequential version of this test passes under that mutation; this one
blocks B on A's row lock (observed via pg_stat_activity) before committing A."
```

---

## Task 4: Epoch and field semantics, and the previous generation is dead

**Files:** Modify `internal/store/retry_job_tasks_integration_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// TestRetryJobTasks_ReopenedRowFields_EpochIncrementsByExactlyOne is item
// criterion 3: assert the increment per row, not merely that the epoch changed.
// The two tasks start at different epochs so a statement that assigned a
// constant would fail.
func TestRetryJobTasks_ReopenedRowFields_EpochIncrementsByExactlyOne(t *testing.T) {
	f := newRetryFixture(t)

	a := f.inStatus(t, "t-a", "failed")
	// Drive b through a requeue first so it lands on a different epoch.
	b := f.inStatus(t, "t-b", "timed_out")
	require.NoError(t, f.q.RequeueTaskByID(f.ctx, b.ID)) // no-op: b is terminal
	b = f.get(t, b.ID)

	before := map[pgtype.UUID]int32{a.ID: a.AssignmentEpoch, b.ID: b.AssignmentEpoch}

	reopened, err := f.q.RetryJobTasks(f.ctx, store.RetryJobTasksParams{
		JobID: f.job.ID, IncludeDone: false,
	})
	require.NoError(t, err)
	require.Len(t, reopened, 2)

	for id, oldEpoch := range before {
		got := f.get(t, id)
		require.Equal(t, "pending", got.Status)
		require.False(t, got.WorkerID.Valid, "worker_id must be cleared")
		require.False(t, got.StartedAt.Valid, "started_at must be cleared")
		require.False(t, got.FinishedAt.Valid, "finished_at must be cleared")
		require.Equal(t, int32(0), got.RetryCount, "retry_count must reset to 0")
		require.Equal(t, oldEpoch+1, got.AssignmentEpoch,
			"assignment_epoch must be old+1 for every reopened row")
	}
}

// TestRetryJobTasks_PreviousGenerationIsDead_StatusLogAndRetryAllRejected is the
// second half of item criterion 3. A retried task's previous generation must be
// unable to write status, logs or a retry. Note WHY each is rejected: the epoch
// no longer matches AND worker_id is now NULL, so the plain `=` comparison fails
// closed. Both are required; neither is decoration.
func TestRetryJobTasks_PreviousGenerationIsDead_StatusLogAndRetryAllRejected(t *testing.T) {
	f := newRetryFixture(t)
	task := f.inStatus(t, "t-failed", "failed")
	oldEpoch, oldWorker := task.AssignmentEpoch, task.WorkerID

	reopened, err := f.q.RetryJobTasks(f.ctx, store.RetryJobTasksParams{
		JobID: f.job.ID, IncludeDone: false,
	})
	require.NoError(t, err)
	require.Len(t, reopened, 1)

	_, err = f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
		ID: task.ID, Status: "done", WorkerID: oldWorker, AssignmentEpoch: oldEpoch,
	})
	require.ErrorIs(t, err, pgx.ErrNoRows, "a late status update from the dead generation must be dropped")

	_, err = f.q.AppendTaskLog(f.ctx, store.AppendTaskLogParams{
		TaskID: task.ID, AssignmentEpoch: oldEpoch, WorkerID: oldWorker,
		Stream: "stdout", Content: "zombie output",
	})
	require.ErrorIs(t, err, pgx.ErrNoRows, "a trailing log chunk from the dead generation must be dropped")

	_, err = f.q.IncrementTaskRetryCount(f.ctx, store.IncrementTaskRetryCountParams{
		ID: task.ID, AssignmentEpoch: oldEpoch, WorkerID: oldWorker,
	})
	require.ErrorIs(t, err, pgx.ErrNoRows, "an agent retry from the dead generation must be dropped")

	logs, err := f.q.GetTaskLogs(f.ctx, task.ID)
	require.NoError(t, err)
	require.Empty(t, logs, "no output from the dead generation may reach task_logs")

	got := f.get(t, task.ID)
	require.Equal(t, "pending", got.Status)
	require.Equal(t, int32(0), got.RetryCount)
	require.Equal(t, oldEpoch+1, got.AssignmentEpoch)
}
```

- [ ] **Step 2: Run them - expect PASS**

Run: `go test -tags integration -p 1 ./internal/store/... -run 'TestRetryJobTasks_(ReopenedRowFields|PreviousGenerationIsDead)' -v -timeout 300s`
Expected: PASS both. The statement already exists, so these are characterization tests; Step 3 supplies their evidence.

- [ ] **Step 3: Three targeted RED proofs, one mutation each**

Each mutation is to `internal/store/query/tasks.sql`, followed by `sqlc generate`, the test run, then `git checkout -- internal/store/query/tasks.sql` + `sqlc generate` + Task 2 Step 5/6 cleanup.

| Mutation on `RetryJobTasks` | Command | Single test expected to redden | Expected message |
| --- | --- | --- | --- |
| Delete `retry_count = 0,` from the `SET` | `-run TestRetryJobTasks_ReopenedRowFields` | `..._ReopenedRowFields_EpochIncrementsByExactlyOne` | `retry_count must reset to 0` |
| Change `assignment_epoch = t.assignment_epoch + 1` to `assignment_epoch = 1` | `-run TestRetryJobTasks_ReopenedRowFields` | same test | `assignment_epoch must be old+1 for every reopened row` (the task seeded at a higher epoch fails) |
| Delete `worker_id = NULL,` from the `SET` | `-run TestRetryJobTasks_PreviousGenerationIsDead` | `..._PreviousGenerationIsDead_...` | this one must **still pass** on the status/retry assertions (the epoch alone rejects them) but must **fail** on `worker_id must be cleared` in the other test - run both and record which fails |

The third row is the honest result and is worth recording as-is: the epoch bump alone is sufficient to kill the previous generation's writes, and the `worker_id` clear is what ends the *assignment*. Both are required by the invariant; the tests show they are separately observable.

- [ ] **Step 4: Commit**

```bash
git add internal/store/retry_job_tasks_integration_test.go
git commit -m "test(store): RetryJobTasks field semantics and the death of the previous generation

Epoch increments by exactly one per reopened row (asserted per row, from two
different starting epochs); worker_id, started_at and finished_at are cleared;
retry_count resets to 0. A status update, a log chunk and an agent retry from
the previous generation are each proven to return pgx.ErrNoRows and to leave
task_logs empty."
```

---

## Task 5: `?task=all` widens to the terminal set and no further, and `GetJobForUpdate` really locks

**Files:** Modify `internal/store/retry_job_tasks_integration_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// TestRetryJobTasks_AllMode_WidensToTerminalSetAndNoFurther is the test that
// stops a retry from evicting a live agent. It must exist even though the
// handler's job-status gate makes a job with a running task hard to reach
// through HTTP: the statement's guarantee must not depend on the caller.
func TestRetryJobTasks_AllMode_WidensToTerminalSetAndNoFurther(t *testing.T) {
	f := newRetryFixture(t)

	pending := f.pending(t, "t-pending")
	dispatched := f.dispatched(t, "t-dispatched")
	running := f.inStatus(t, "t-running", "running")
	done := f.inStatus(t, "t-done", "done")
	failed := f.inStatus(t, "t-failed", "failed")
	timedOut := f.inStatus(t, "t-timedout", "timed_out")

	reopened, err := f.q.RetryJobTasks(f.ctx, store.RetryJobTasksParams{
		JobID: f.job.ID, IncludeDone: true,
	})
	require.NoError(t, err)
	require.ElementsMatch(t, []pgtype.UUID{done.ID, failed.ID, timedOut.ID}, reopened,
		"task=all must reopen exactly the three terminal statuses")

	for _, live := range []store.Task{pending, dispatched, running} {
		got := f.get(t, live.ID)
		require.Equal(t, live.Status, got.Status, "a non-terminal task must be untouched")
		require.Equal(t, live.AssignmentEpoch, got.AssignmentEpoch,
			"a non-terminal task's generation must not be ended - that would evict a live agent")
		require.Equal(t, live.WorkerID, got.WorkerID)
	}
}

// TestGetJobForUpdate_TakesARowLockThatBlocksASecondReader proves the statement
// is not just GetJob with a longer name. Without the lock, handleCancelJob and
// handleRetryJob interleave and a cancel can stamp `cancelled` over a retry's
// freshly pending tasks - and GetEligibleTasks does not consult job status.
func TestGetJobForUpdate_TakesARowLockThatBlocksASecondReader(t *testing.T) {
	f := newRetryFixture(t)

	txA, err := f.pool.Begin(f.ctx)
	require.NoError(t, err)
	defer txA.Rollback(f.ctx)
	_, err = f.q.WithTx(txA).GetJobForUpdate(f.ctx, f.job.ID)
	require.NoError(t, err)

	txB, err := f.pool.Begin(f.ctx)
	require.NoError(t, err)
	defer txB.Rollback(f.ctx)

	blocked := make(chan error, 1)
	go func() {
		_, err := f.q.WithTx(txB).GetJobForUpdate(f.ctx, f.job.ID)
		blocked <- err
	}()

	select {
	case err := <-blocked:
		t.Fatalf("second GetJobForUpdate returned immediately (%v): the row lock is missing", err)
	case <-time.After(750 * time.Millisecond):
	}

	require.NoError(t, txA.Commit(f.ctx))
	select {
	case err := <-blocked:
		require.NoError(t, err, "B must succeed once A commits")
	case <-time.After(10 * time.Second):
		t.Fatal("B never unblocked after A committed")
	}
}
```

- [ ] **Step 2: Run them**

Run: `go test -tags integration -p 1 ./internal/store/... -run 'TestRetryJobTasks_AllMode|TestGetJobForUpdate' -v -timeout 300s`
Expected: PASS both.

- [ ] **Step 3: RED proofs**

| Mutation | Single test reddening | Expected message |
| --- | --- | --- |
| In `RetryJobTasks`'s row-level `WHERE`, widen to `t.status IN ('failed','timed_out','running')` | `TestRetryJobTasks_AllMode_WidensToTerminalSetAndNoFurther` | `a non-terminal task's generation must not be ended` |
| In `jobs.sql`, drop `FOR UPDATE` from `GetJobForUpdate` | `TestGetJobForUpdate_TakesARowLockThatBlocksASecondReader` | `second GetJobForUpdate returned immediately ...: the row lock is missing` |

For each: apply, `sqlc generate`, run the single test, observe FAIL, then `git checkout -- internal/store/query/<file>`, `sqlc generate`, Task 2 Step 5/6 cleanup, re-run (PASS).

- [ ] **Step 4: Commit**

```bash
git add internal/store/retry_job_tasks_integration_test.go
git commit -m "test(store): task=all widens to the terminal set only, and GetJobForUpdate locks

A job with one task in each of the six statuses: include_done reopens exactly
done, failed and timed_out; pending, dispatched and running keep their status,
epoch and assignee, so a retry can never evict a live agent. Separately, a
second GetJobForUpdate blocks until the first transaction commits - proven RED
by dropping FOR UPDATE."
```

---

## Task 6: The dependents guard - negative, positive, transitive, all-or-nothing

Tests 1 and 2 below are a **matched pair** and neither is meaningful alone: a guard reading `dep.status <> 'pending'` with no `NOT IN (selected)` exclusion passes the negative test and refuses every real retry in production, so the feature would be inert and look tested.

**Files:** Modify `internal/store/retry_job_tasks_integration_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// NEGATIVE control. T failed, its dependent D already ran (done) and is not in
// the selected set. Reopening T under D would reproduce route B of
// bug-2026-06-26 by design.
func TestRetryJobTasks_DependentsGuard_AlreadyRanDependentBlocks(t *testing.T) {
	f := newRetryFixture(t)
	tsk := f.inStatus(t, "t", "failed")
	d := f.inStatus(t, "d", "done")
	f.dep(t, d, tsk) // d depends on t

	reopened, err := f.q.RetryJobTasks(f.ctx, store.RetryJobTasksParams{
		JobID: f.job.ID, IncludeDone: false,
	})
	require.NoError(t, err)
	require.Empty(t, reopened, "a task whose dependent already ran must not reopen")
	require.Equal(t, "failed", f.get(t, tsk.ID).Status)
	require.Equal(t, tsk.AssignmentEpoch, f.get(t, tsk.ID).AssignmentEpoch)
}

// POSITIVE control - the one that catches a guard that blocks everything. On any
// healthy failed job FailDependentTasks has cascade-failed the failing task's
// pending dependents, so every selected task HAS failed dependents. A dependent
// that is itself being reopened by this same request must not block.
func TestRetryJobTasks_DependentsGuard_CascadeFailedDependentDoesNotBlock(t *testing.T) {
	f := newRetryFixture(t)
	tsk := f.inStatus(t, "t", "failed")
	d := f.pending(t, "d")
	f.dep(t, d, tsk)
	require.NoError(t, f.q.FailDependentTasks(f.ctx, tsk.ID))
	require.Equal(t, "failed", f.get(t, d.ID).Status)

	reopened, err := f.q.RetryJobTasks(f.ctx, store.RetryJobTasksParams{
		JobID: f.job.ID, IncludeDone: false,
	})
	require.NoError(t, err)
	require.ElementsMatch(t, []pgtype.UUID{tsk.ID, d.ID}, reopened,
		"the ordinary retry case: a cascade-failed dependent is reopened alongside its dependency")
	require.Equal(t, "pending", f.get(t, tsk.ID).Status)
	require.Equal(t, "pending", f.get(t, d.ID).Status)
}

// TRANSITIVE. A -> B -> C (C depends on B, B depends on A). A failed, B failed,
// C done. Direct edges alone would miss C, because C is not a direct dependent
// of A. Proves the descendant closure.
func TestRetryJobTasks_DependentsGuard_TransitiveDescendantBlocks(t *testing.T) {
	f := newRetryFixture(t)
	a := f.inStatus(t, "a", "failed")
	b := f.inStatus(t, "b", "failed")
	c := f.inStatus(t, "c", "done")
	f.dep(t, b, a)
	f.dep(t, c, b)

	reopened, err := f.q.RetryJobTasks(f.ctx, store.RetryJobTasksParams{
		JobID: f.job.ID, IncludeDone: false,
	})
	require.NoError(t, err)
	require.Empty(t, reopened, "a transitive descendant that already ran must block the whole request")

	withDone, err := f.q.RetryJobTasks(f.ctx, store.RetryJobTasksParams{
		JobID: f.job.ID, IncludeDone: true,
	})
	require.NoError(t, err)
	require.ElementsMatch(t, []pgtype.UUID{a.ID, b.ID, c.ID}, withDone,
		"with C selected too, the closure is fully covered and all three reopen")
}

// ALL-OR-NOTHING. Same shape, asserting NO row changed rather than merely that
// the count was zero. A per-row guard would reopen B and strand it: B would be
// pending behind a terminal A, GetEligibleTasks requires every dependency to be
// done, so B would never dispatch again and the job would sit at running forever.
func TestRetryJobTasks_DependentsGuard_ExclusionIsAllOrNothing(t *testing.T) {
	f := newRetryFixture(t)
	a := f.inStatus(t, "a", "failed")
	b := f.inStatus(t, "b", "failed")
	c := f.inStatus(t, "c", "done")
	f.dep(t, b, a)
	f.dep(t, c, b)

	before := map[pgtype.UUID]store.Task{a.ID: a, b.ID: b, c.ID: c}
	reopened, err := f.q.RetryJobTasks(f.ctx, store.RetryJobTasksParams{
		JobID: f.job.ID, IncludeDone: false,
	})
	require.NoError(t, err)
	require.Empty(t, reopened)

	for id, was := range before {
		got := f.get(t, id)
		require.Equal(t, was.Status, got.Status, "no row may change under a blocked retry")
		require.Equal(t, was.AssignmentEpoch, got.AssignmentEpoch)
		require.Equal(t, was.WorkerID, got.WorkerID)
		require.Equal(t, was.RetryCount, got.RetryCount)
	}
}

// SelectRetryableTaskIDs must NOT carry the dependents guard: if it did, the
// handler could not tell "no failed tasks" from "blocked by a dependent", and
// would report the former for a job that has several failed tasks.
func TestSelectRetryableTaskIDs_IsUnguardedAndMatchesRetryJobTasksSelection(t *testing.T) {
	f := newRetryFixture(t)
	tsk := f.inStatus(t, "t", "failed")
	d := f.inStatus(t, "d", "done")
	f.dep(t, d, tsk)

	selected, err := f.q.SelectRetryableTaskIDs(f.ctx, store.SelectRetryableTaskIDsParams{
		JobID: f.job.ID, IncludeDone: false,
	})
	require.NoError(t, err)
	require.Equal(t, []pgtype.UUID{tsk.ID}, selected,
		"the selection is unguarded: it reports what the mode matched, not what the guard allowed")

	reopened, err := f.q.RetryJobTasks(f.ctx, store.RetryJobTasksParams{
		JobID: f.job.ID, IncludeDone: false,
	})
	require.NoError(t, err)
	require.Empty(t, reopened, "the guard blocks here, and the difference is what the handler classifies")

	all, err := f.q.SelectRetryableTaskIDs(f.ctx, store.SelectRetryableTaskIDsParams{
		JobID: f.job.ID, IncludeDone: true,
	})
	require.NoError(t, err)
	require.ElementsMatch(t, []pgtype.UUID{tsk.ID, d.ID}, all,
		"include_done widens the selection identically to RetryJobTasks's")
}
```

- [ ] **Step 2: Run them**

Run: `go test -tags integration -p 1 ./internal/store/... -run 'TestRetryJobTasks_DependentsGuard|TestSelectRetryableTaskIDs' -v -timeout 600s`
Expected: PASS all five.

- [ ] **Step 3: RED proofs, one mutation each**

| Mutation on `RetryJobTasks` | Single test reddening | Expected message |
| --- | --- | --- |
| Delete the whole `AND NOT EXISTS (...)` conjunct | `..._AlreadyRanDependentBlocks` | `a task whose dependent already ran must not reopen` |
| Delete `AND d.id NOT IN (SELECT id FROM selected)` from the guard | `..._CascadeFailedDependentDoesNotBlock` | `the ordinary retry case: ...` (the guard now refuses every real retry) |
| Replace the recursive `descendants` CTE with direct edges only (drop the `UNION` and the recursive term) | `..._TransitiveDescendantBlocks` | `a transitive descendant that already ran must block the whole request` |
| Add the `NOT EXISTS` guard to `SelectRetryableTaskIDs` | `TestSelectRetryableTaskIDs_IsUnguardedAndMatchesRetryJobTasksSelection` | `the selection is unguarded: ...` |

Note the second and third mutations do **not** redden the first test - that separation is what makes the pair strong rather than coupled. Record which tests stay green under each mutation; if a mutation reddens more than its named test, report the coupling.

`..._ExclusionIsAllOrNothing` shares a mutation with the transitive test by construction (it is the same DAG). Its distinct value is the assertion shape (no row changed, not "count was zero"), so pin it separately with a **per-row guard** mutation: change the `NOT EXISTS` subquery to correlate on the row under update by adding `AND d.id <> t.id`... **do not ship this**; it is only a mutation. Expected: `..._ExclusionIsAllOrNothing` fails on `no row may change under a blocked retry` because B reopens while A stays terminal.

- [ ] **Step 4: Commit**

```bash
git add internal/store/retry_job_tasks_integration_test.go
git commit -m "test(store): the dependents guard, with matched negative and positive controls

A dependent that already ran blocks; a cascade-failed dependent being reopened by
the same request does not. A transitive descendant blocks too, proving the
closure. Exclusion is all-or-nothing: no row changes, not merely a zero count.
SelectRetryableTaskIDs stays unguarded so the handler can tell 'nothing matched'
from 'blocked'. Each proven RED by its own single mutation."
```

---

## Task 7: Extract `jobOwnerOr404`, and make `handleCancelJob` lock the job row first

Behavior-preserving refactor. The gate this extracts would otherwise exist twice, and duplicating an authorization gate is how two copies diverge. The `GetJobForUpdate` swap is **required scope**, not hardening (spec finding 6): without it, cancel and retry take their row locks in opposite orders - an ABBA pair between two ordinary operator actions - and a cancel running against a pre-retry snapshot matches no tasks and then stamps the job `cancelled` over a retry's freshly `pending` tasks.

**Gate for this task: every existing assertion in `internal/api/jobs_cancel_test.go` must stay byte-identical.** An assertion needing adjustment is a finding about the change, not a test to update.

**Files:** Modify `internal/api/jobs.go:676-780`

- [ ] **Step 1: Establish the green baseline and record it**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestCancelJob -v -timeout 600s`
Expected: PASS (5 tests plus the 8 `TestCancelJob_Force_QueryParamParsing` subtests). Save the output; Step 4 compares against it.

- [ ] **Step 2: Extract the gate and swap the read**

In `internal/api/jobs.go`, insert immediately above `func (s *Server) handleCancelJob`:

```go
// jobOwnerOr404 is the shared owner-or-admin gate for the two destructive
// job-scoped writes, cancel and retry. It takes a job row the caller has already
// read INSIDE its own transaction, so the decision is made against the same
// snapshot the write will use.
//
// It writes the response and returns false when the caller may not act; callers
// simply return, which rolls back their open transaction and leaves nothing
// written. A non-owner non-admin gets 404, not 403, matching ownedScheduledJob.
// See the Jobs block comment in server.go for why that is defense-in-depth
// rather than a true existence secret: the GET routes are global.
//
// There is exactly one copy of this gate. Do not inline a second one.
//
// The parameter order (w, ctx, job) is the spec's and is deliberate; it is the
// one place in this file where context.Context is not the first parameter.
func (s *Server) jobOwnerOr404(w http.ResponseWriter, ctx context.Context, job store.Job) bool {
	u, ok := UserFromCtx(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	if !u.IsAdmin && job.SubmittedBy != u.ID {
		writeError(w, http.StatusNotFound, "job not found")
		return false
	}
	return true
}
```

In `handleCancelJob`, change line 694 from `job, err := q.GetJob(ctx, id)` to:

```go
	// Lock the job row FIRST, before touching any task row. handleRetryJob does
	// the same. The single lock order (job, then tasks) is what keeps the two
	// handlers from being an ABBA deadlock pair, and what makes a retry landing
	// in this request's window serialize instead of interleave. See
	// GetJobForUpdate.
	job, err := q.GetJobForUpdate(ctx, id)
```

and replace the gate at `:704-715` (the comment plus the `u, ok := UserFromCtx` block) with:

```go
	// Owner-or-admin gate. Returning here rolls back the open tx, so no task is
	// cancelled and no agent signal is sent.
	if !s.jobOwnerOr404(w, ctx, job) {
		return
	}
```

- [ ] **Step 3: Run the cancel tests**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestCancelJob -v -timeout 600s`
Expected: PASS, identical set to Step 1.

- [ ] **Step 4: Prove no test file changed**

Run: `git diff --stat -- '*_test.go'`
Expected: **empty output**. If any test file appears, STOP and report: the refactor changed behavior.

Run: `go test -tags integration -p 1 ./internal/api/... -timeout 900s` and `go vet -tags integration ./...`
Expected: PASS, clean.

- [ ] **Step 5: Commit**

```bash
git add internal/api/jobs.go
git commit -m "refactor(api): extract jobOwnerOr404 and lock the job row first in cancel

handleCancelJob now reads through GetJobForUpdate rather than GetJob, so both
multi-statement writers over jobs+tasks take the job row lock before any task
row. Without it, adding a job-then-tasks retry writer creates an ABBA deadlock
pair, and a cancel running against a pre-retry snapshot stamps the job cancelled
over freshly pending tasks that GetEligibleTasks will happily dispatch.

Behavior-preserving: every existing cancel test passes byte-identical."
```

---

## Task 8: The route, the handler skeleton, `?task` parsing and the auth gate

**Files:** Modify `internal/api/server.go:105-122`; modify `internal/api/jobs.go`; create `internal/api/jobs_retry_integration_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/api/jobs_retry_integration_test.go`:

```go
//go:build integration

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"relay/internal/api"
	"relay/internal/events"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type retryEnv struct {
	srv  *api.Server
	q    *store.Queries
	pool *pgxpool.Pool
	w    store.Worker
}

func newRetryEnv(t *testing.T) *retryEnv {
	t.Helper()
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)
	w, err := q.CreateWorker(t.Context(), store.CreateWorkerParams{
		Name: "retry-w", Hostname: "retry-host", CpuCores: 4, RamGb: 8, Os: "linux",
	})
	require.NoError(t, err)
	return &retryEnv{srv: srv, q: q, pool: pool, w: w}
}

func (e *retryEnv) job(t *testing.T, owner pgtype.UUID) store.Job {
	t.Helper()
	job, err := e.q.CreateJob(t.Context(), store.CreateJobParams{
		Name: "retry-job", Priority: "normal", SubmittedBy: owner, Labels: []byte("{}"),
	})
	require.NoError(t, err)
	return job
}

// task drives a new task of job to status through the production path, so the
// row carries a real assignee and epoch. status may be pending, dispatched,
// running, done, failed or timed_out.
func (e *retryEnv) task(t *testing.T, job store.Job, name, status string) store.Task {
	t.Helper()
	ctx := t.Context()
	task, err := e.q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: name, Commands: []byte(`[["echo","x"]]`),
		Env: []byte("{}"), Requires: []byte("{}"), Retries: 0,
	})
	require.NoError(t, err)
	if status == "pending" {
		return task
	}
	claimed, err := e.q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: e.w.ID,
	})
	require.NoError(t, err)
	if status == "dispatched" {
		return claimed
	}
	updated, err := e.q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
		ID: claimed.ID, Status: status, WorkerID: claimed.WorkerID,
		AssignmentEpoch: claimed.AssignmentEpoch,
	})
	require.NoError(t, err)
	return updated
}

// recompute settles the job's status from its tasks, the way every production
// task transition does. Without it the job would still be `pending` and the
// handler's job-status gate would 409 for the wrong reason.
func (e *retryEnv) recompute(t *testing.T, job store.Job) string {
	t.Helper()
	st, err := e.q.RecomputeJobStatus(t.Context(), job.ID)
	require.NoError(t, err)
	return st
}

func (e *retryEnv) do(t *testing.T, token, jobID, query string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/v1/jobs/" + jobID + "/retry"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodPost, url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func (e *retryEnv) getTask(t *testing.T, id pgtype.UUID) store.Task {
	t.Helper()
	got, err := e.q.GetTask(t.Context(), id)
	require.NoError(t, err)
	return got
}

func (e *retryEnv) getJob(t *testing.T, id pgtype.UUID) store.Job {
	t.Helper()
	got, err := e.q.GetJob(t.Context(), id)
	require.NoError(t, err)
	return got
}

func errBody(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	s, _ := body["error"].(string)
	return s
}

func TestRetryJob_Unauthenticated_401(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "retry-401@example.com", false)
	job := e.job(t, owner.ID)
	e.task(t, job, "t", "failed")
	e.recompute(t, job)

	rec := e.do(t, "", uuidString(job.ID), "task=failed")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "failed", e.getJob(t, job.ID).Status)
}

func TestRetryJob_NonOwner_404_NoSideEffects(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "retry-victim@example.com", false)
	attacker := createTestUser(t, e.q, "Attacker", "retry-attacker@example.com", false)
	token := createTestToken(t, e.q, attacker.ID)
	job := e.job(t, owner.ID)
	task := e.task(t, job, "t", "failed")
	e.recompute(t, job)

	rec := e.do(t, token, uuidString(job.ID), "task=failed")
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "job not found", errBody(t, rec))

	// A 404 that still performed the write is the failure this test exists for.
	got := e.getTask(t, task.ID)
	assert.Equal(t, "failed", got.Status)
	assert.Equal(t, task.AssignmentEpoch, got.AssignmentEpoch)
	assert.Equal(t, "failed", e.getJob(t, job.ID).Status)
}

func TestRetryJob_TaskParam_Rejects(t *testing.T) {
	cases := []struct{ name, query string }{
		{"absent", ""},
		{"empty", "task="},
		{"wrong_case", "task=Failed"},
		{"unrecognized", "task=everything"},
		{"repeated", "task=failed&task=all"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newRetryEnv(t)
			owner := createTestUser(t, e.q, "Owner",
				fmt.Sprintf("retry-parse-%s@example.com", tc.name), false)
			token := createTestToken(t, e.q, owner.ID)
			job := e.job(t, owner.ID)
			task := e.task(t, job, "t", "failed")
			e.recompute(t, job)

			rec := e.do(t, token, uuidString(job.ID), tc.query)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, errBody(t, rec), `"task" is required`)

			got := e.getTask(t, task.ID)
			assert.Equal(t, "failed", got.Status, "a rejected request must write nothing")
			assert.Equal(t, task.AssignmentEpoch, got.AssignmentEpoch)
		})
	}
}

// The 400 must be indistinguishable for an existing and a non-existent job:
// parsing happens before any database work, so a malformed request leaks nothing
// and costs nothing.
func TestRetryJob_TaskParam_400_BeforeAnyDatabaseWork(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "retry-nodb@example.com", false)
	token := createTestToken(t, e.q, owner.ID)

	rec := e.do(t, token, "0d1b2f3e-4a5b-6c7d-8e9f-0a1b2c3d4e5f", "task=nonsense")
	require.Equal(t, http.StatusBadRequest, rec.Code,
		"an unparseable mode must 400, not 404, even for a job that does not exist")

	rec = e.do(t, token, "not-a-uuid", "task=failed")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "invalid job id", errBody(t, rec))
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestRetryJob_ -v -timeout 900s`
Expected: **FAIL**. Every case returns **404** (no `mux` pattern matches `POST /v1/jobs/{id}/retry`; `StaticHandler` is nil in tests). That is a **weak RED** - it proves the route is absent, not that the handler is right. The discriminating evidence is `TestRetryJob_TaskParam_Rejects` and `..._400_BeforeAnyDatabaseWork`, which fail against a handler that parses with `Query().Get()` or defaults the mode.

- [ ] **Step 3: Register the route and correct the block comment**

In `internal/api/server.go`, replace the sentence at `:110` (`	// Only the destructive cancel (DELETE) is owner-or-admin gated.`) with:

```go
	// The two destructive writes - cancel (DELETE) and retry
	// (POST /v1/jobs/{id}/retry) - are owner-or-admin gated inside their
	// handlers, via the single jobOwnerOr404 helper in jobs.go.
```

and in the following paragraph change `handleCancelJob returns 404` to `handleCancelJob and handleRetryJob return 404`.

Add after line 122 (`mux.Handle("DELETE /v1/jobs/{id}", ...)`):

```go
	mux.Handle("POST /v1/jobs/{id}/retry", auth(http.HandlerFunc(s.handleRetryJob)))
```

- [ ] **Step 4: Write the handler**

In `internal/api/jobs.go`, add `"log"` to the import block, and append at the end of the file:

```go
// retryJobResponse is the body returned by POST /v1/jobs/{id}/retry. It embeds
// jobResponse (its fields flatten into the JSON object) and adds one key, the
// same shape disableWorkerResponse uses for requeued_tasks. tasks_retried is
// always >= 1 on a 200: a zero-match retry is a 409, never a successful no-op,
// so a client never has to tell a no-op from a real re-run by reading a number.
type retryJobResponse struct {
	jobResponse
	TasksRetried int `json:"tasks_retried"`
}

// uuidStrList renders task ids for the two server-side diagnostic log lines.
// The blocked ids belong in the log, not in the error body: every handler in
// this codebase errors through writeError into {"error": string}, and inventing
// a second error shape for one endpoint is a bigger change than the diagnosis is
// worth. The per-task detail is one GET /v1/jobs/{id} away.
func uuidStrList(ids []pgtype.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = uuidStr(id)
	}
	return out
}

// handleRetryJob returns a finished job's tasks to the queue so the farm re-runs
// them. See docs/superpowers/specs/2026-08-13-job-retry-endpoint.md.
//
// This is a fenced multi-row write on tasks. The ordering below is load-bearing
// and every 4xx/5xx path returns before the commit, so the deferred rollback
// undoes any write - which is what makes "nothing was applied" literally true.
//
// No request body: ?task= is a query parameter, matching ?force= on the cancel
// sibling. readJSON is never called and must not be added.
func (s *Server) handleRetryJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job id")
		return
	}

	// ?task is required, single-valued and matched exactly. Query().Get() would
	// silently return the first of a repeated parameter, and ParseBool-style
	// leniency is wrong here: ?force=garbage fails safe to "graceful", while a
	// misread here means "re-ran everything". Parsed BEFORE any database work,
	// so a malformed request costs nothing and returns the same 400 for an
	// existing and a non-existent job.
	vals := r.URL.Query()["task"]
	if len(vals) != 1 || (vals[0] != "failed" && vals[0] != "all") {
		writeError(w, http.StatusBadRequest,
			`query parameter "task" is required and must be exactly "failed" or "all"`)
		return
	}
	mode := vals[0]
	includeDone := mode == "all"

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer tx.Rollback(ctx)
	q := s.q.WithTx(tx)

	// Lock the job row FIRST, before touching any task row. handleCancelJob does
	// the same; see GetJobForUpdate for the two properties that depend on it.
	job, err := q.GetJobForUpdate(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "job not found")
		} else {
			writeError(w, http.StatusInternalServerError, "db error")
		}
		return
	}

	if !s.jobOwnerOr404(w, ctx, job) {
		return
	}

	// Retry requires a finished job. Because this gate admits only done and
	// failed, the ONLY job-status transition this endpoint can cause is
	// done|failed -> running, so RecomputeJobStatus - which has no notion of
	// `cancelled` - is unreachable from a cancelled job through this path. That
	// is a stronger property than fixing its CASE would give, and it is
	// verifiable by reading these eight lines.
	//
	// A cancelled job is refused rather than un-cancelled: CancelJobTasks
	// squashes cancellation onto `failed`, so ?task=failed on a cancelled job
	// would select every task that was in flight when the cancel landed. "Retry"
	// would silently mean "un-cancel everything".
	switch job.Status {
	case "done", "failed":
	case "cancelled":
		writeError(w, http.StatusConflict,
			"job was cancelled; retry is not available for a cancelled job")
		return
	default:
		writeError(w, http.StatusConflict,
			"job is not finished; retry is available for a done or failed job")
		return
	}

	selected, err := q.SelectRetryableTaskIDs(ctx, store.SelectRetryableTaskIDsParams{
		JobID: id, IncludeDone: includeDone,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if len(selected) == 0 {
		if includeDone {
			writeError(w, http.StatusConflict,
				"no tasks matched task=all; this job has no finished tasks")
		} else {
			writeError(w, http.StatusConflict,
				"no tasks matched task=failed; this job has no failed or timed_out tasks")
		}
		return
	}

	reopened, err := q.RetryJobTasks(ctx, store.RetryJobTasksParams{
		JobID: id, IncludeDone: includeDone,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	// The dependents guard is all-or-nothing by construction (its NOT EXISTS is
	// uncorrelated), so zero-against-nonzero is the guard and any other mismatch
	// is provably concurrency. That structural argument is why no extra query is
	// needed to classify these two cases apart.
	if len(reopened) == 0 {
		log.Printf("api: retry job %s task=%s blocked by dependents: selected=%v",
			uuidStr(id), mode, uuidStrList(selected))
		writeError(w, http.StatusConflict,
			"no tasks were reopened: a selected task has dependents that have already run, "+
				"or the job changed while the request was in flight; nothing was applied")
		return
	}
	if len(reopened) != len(selected) {
		log.Printf("api: retry job %s task=%s raced: selected=%v reopened=%v",
			uuidStr(id), mode, uuidStrList(selected), uuidStrList(reopened))
		writeError(w, http.StatusConflict,
			"the job changed while the retry was in flight; nothing was applied - try again")
		return
	}

	// By construction this returns 'running': at least one task is now pending.
	if _, err := q.RecomputeJobStatus(ctx, id); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	// Re-read inside the transaction so the response carries the recomputed
	// status and the new updated_at; RecomputeJobStatus returns only the status.
	job, err = q.GetJob(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	// Wake the dispatcher from INSIDE the transaction. Postgres queues pg_notify
	// payloads until commit, so this side effect is gated on BOTH the row count
	// (we only reach here with len(reopened) == len(selected) >= 1) and on the
	// transaction actually committing - a strictly stronger form of "gate any
	// side effect on the fence having matched" than a post-commit call. Same
	// shape as the requeue path in workers.go.
	if err := q.NotifyTaskSubmitted(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	// After commit, matching handleCancelJob. Unlike cancel there is no agent
	// signal to send: every reopened row was terminal, so no agent holds it.
	s.broker.Publish(events.Event{
		Type:  "job",
		JobID: uuidStr(job.ID),
		Data:  []byte(`{"status":"running"}`),
	})

	writeJSON(w, http.StatusOK, retryJobResponse{
		jobResponse:  toJobResponse(job, "", nil, nil),
		TasksRetried: len(reopened),
	})
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestRetryJob_ -v -timeout 900s`
Expected: PASS (4 tests, 5 subtests).

- [ ] **Step 6: Prove the parsing tests are not vacuous**

Mutation: replace the `vals := ...` block in `handleRetryJob` with the lenient spelling:

```go
	mode := r.URL.Query().Get("task")
	if mode == "" {
		mode = "all"
	}
	includeDone := mode != "failed"
```

Run: `go test -tags integration -p 1 ./internal/api/... -run TestRetryJob_TaskParam -v -timeout 900s`
Expected: **FAIL** on `absent`, `empty`, `wrong_case`, `unrecognized` and `repeated` (each gets a 200 or a 409 instead of a 400), and on `..._400_BeforeAnyDatabaseWork` (which now 404s the nonexistent job). Revert with `git checkout -- internal/api/jobs.go`, re-apply Step 4, re-run Step 5.

- [ ] **Step 7: Commit**

```bash
git add internal/api/jobs.go internal/api/server.go internal/api/jobs_retry_integration_test.go
git commit -m "feat(api): POST /v1/jobs/{id}/retry - route, handler, parsing and gating

?task is required, single-valued and matched exactly; absent, empty, repeated,
wrong-case and unrecognized values are each a 400 written before any database
work, so a malformed request leaks nothing. Owner-or-admin through the shared
jobOwnerOr404, 404 on deny, with the job row locked first. Corrects the Jobs
block comment, which claimed cancel was the only owner-or-admin gated write."
```

---

## Task 9: The success path - 200, response shape, `failed` versus `all`, recomputed status

**Files:** Modify `internal/api/jobs_retry_integration_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestRetryJob_Owner_200(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "retry-owner@example.com", false)
	token := createTestToken(t, e.q, owner.ID)
	job := e.job(t, owner.ID)
	task := e.task(t, job, "t", "failed")
	require.Equal(t, "failed", e.recompute(t, job))

	rec := e.do(t, token, uuidString(job.ID), "task=failed")
	require.Equal(t, http.StatusOK, rec.Code)

	got := e.getTask(t, task.ID)
	assert.Equal(t, "pending", got.Status)
	assert.Equal(t, task.AssignmentEpoch+1, got.AssignmentEpoch)
	assert.False(t, got.WorkerID.Valid)
}

func TestRetryJob_Admin_200_OnAnotherUsersJob(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "retry-admin-owner@example.com", false)
	admin := createTestUser(t, e.q, "Admin", "retry-admin@example.com", true)
	token := createTestToken(t, e.q, admin.ID)
	job := e.job(t, owner.ID)
	e.task(t, job, "t", "failed")
	e.recompute(t, job)

	rec := e.do(t, token, uuidString(job.ID), "task=failed")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "running", e.getJob(t, job.ID).Status)
}

// Item criterion 1: the two modes select demonstrably different sets. Two
// identically seeded jobs, so the difference cannot come from ordering.
func TestRetryJob_FailedVersusAll_SelectDifferentSets(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "retry-modes@example.com", false)
	token := createTestToken(t, e.q, owner.ID)

	seed := func(name string) (store.Job, store.Task, store.Task) {
		job := e.job(t, owner.ID)
		done := e.task(t, job, name+"-done", "done")
		failed := e.task(t, job, name+"-failed", "failed")
		require.Equal(t, "failed", e.recompute(t, job))
		return job, done, failed
	}
	jobA, doneA, _ := seed("a")
	jobB, doneB, _ := seed("b")

	recA := e.do(t, token, uuidString(jobA.ID), "task=failed")
	require.Equal(t, http.StatusOK, recA.Code)
	var bodyA map[string]any
	require.NoError(t, json.NewDecoder(recA.Body).Decode(&bodyA))
	assert.Equal(t, float64(1), bodyA["tasks_retried"])
	assert.Equal(t, "done", e.getTask(t, doneA.ID).Status, "task=failed must leave a done task terminal")

	recB := e.do(t, token, uuidString(jobB.ID), "task=all")
	require.Equal(t, http.StatusOK, recB.Code)
	var bodyB map[string]any
	require.NoError(t, json.NewDecoder(recB.Body).Decode(&bodyB))
	assert.Equal(t, float64(2), bodyB["tasks_retried"])
	assert.Equal(t, "pending", e.getTask(t, doneB.ID).Status, "task=all must reopen the done task too")
}

// Key-set equality, so an accidentally added or renamed field fails. The absent
// keys are jobResponse's omitempty fields, which toJobResponse(job, "", nil, nil)
// leaves unset - exactly as handleCancelJob does.
func TestRetryJob_ResponseShape_JobPlusTasksRetried(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "retry-shape@example.com", false)
	token := createTestToken(t, e.q, owner.ID)
	job := e.job(t, owner.ID)
	e.task(t, job, "t", "failed")
	e.recompute(t, job)

	rec := e.do(t, token, uuidString(job.ID), "task=failed")
	require.Equal(t, http.StatusOK, rec.Code)

	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))

	keys := make([]string, 0, len(body))
	for k := range body {
		keys = append(keys, k)
	}
	require.ElementsMatch(t, []string{
		"id", "name", "priority", "status", "submitted_by", "labels",
		"created_at", "updated_at", "total_tasks", "done_tasks", "tasks_retried",
	}, keys, "the 200 body is jobResponse plus exactly one key")

	assert.Equal(t, "running", body["status"])
	assert.GreaterOrEqual(t, body["tasks_retried"], float64(1),
		"tasks_retried is never 0 on a 200: a zero match is a 409")
	assert.Equal(t, uuidString(job.ID), body["id"])
}

func TestRetryJob_JobStatusRecomputedToRunningInsideTheTransaction(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "retry-recompute@example.com", false)
	token := createTestToken(t, e.q, owner.ID)
	job := e.job(t, owner.ID)
	e.task(t, job, "t", "failed")
	require.Equal(t, "failed", e.recompute(t, job))
	before := e.getJob(t, job.ID)

	rec := e.do(t, token, uuidString(job.ID), "task=failed")
	require.Equal(t, http.StatusOK, rec.Code)

	after := e.getJob(t, job.ID)
	assert.Equal(t, "running", after.Status,
		"a job whose task is pending again cannot still be failed")
	assert.True(t, after.UpdatedAt.Time.After(before.UpdatedAt.Time),
		"RecomputeJobStatus stamps updated_at, and it ran in the same transaction as the reopen")
}
```

- [ ] **Step 2: Run to verify they pass**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestRetryJob_ -v -timeout 900s`
Expected: PASS.

- [ ] **Step 3: RED proofs**

| Mutation in `handleRetryJob` | Single test reddening | Expected message |
| --- | --- | --- |
| `TasksRetried: len(reopened)` -> `TasksRetried: 0` | `..._ResponseShape_JobPlusTasksRetried` | `tasks_retried is never 0 on a 200` |
| Add a field `Foo string \`json:"foo"\`` to `retryJobResponse` | `..._ResponseShape_JobPlusTasksRetried` | ElementsMatch failure listing `foo` |
| Delete the `RecomputeJobStatus` call and the `GetJob` re-read (respond with the locked `job`) | `..._JobStatusRecomputedToRunningInsideTheTransaction` | `a job whose task is pending again cannot still be failed` |
| `includeDone := mode == "all"` -> `includeDone := true` | `..._FailedVersusAll_SelectDifferentSets` | `task=failed must leave a done task terminal` |

Apply, run only the named test, observe FAIL, `git checkout -- internal/api/jobs.go`, re-run.

- [ ] **Step 4: Commit**

```bash
git add internal/api/jobs_retry_integration_test.go
git commit -m "test(api): retry success path - shape, modes, and the recomputed job status

?task=failed and ?task=all select demonstrably different sets on two identically
seeded jobs. The 200 body is jobResponse plus exactly one key, asserted by
key-set equality so an added field fails, and tasks_retried is never 0."
```

---

## Task 10: The 409 family - cancelled, non-terminal, zero matched, blocked, and the deterministic case C

**Files:** Modify `internal/api/jobs_retry_integration_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// installSkipUpdateTrigger makes UPDATEs on the named task a silent no-op, so
// RetryJobTasks matches it but produces no RETURNING row. This is the ONLY
// deterministic way to reach the handler's count-mismatch branch: two concurrent
// retries cannot, because both take the job row lock first and fully serialize
// (see the plan's Deviations section). Modeled on installFailDeleteTrigger.
func installSkipUpdateTrigger(t *testing.T, pool *pgxpool.Pool, taskName string) {
	t.Helper()
	_, err := pool.Exec(t.Context(), fmt.Sprintf(`
		CREATE OR REPLACE FUNCTION skip_update_task() RETURNS trigger AS $$
		BEGIN
		  IF NEW.name = %s THEN RETURN NULL; END IF;
		  RETURN NEW;
		END;
		$$ LANGUAGE plpgsql;
		CREATE TRIGGER skip_update_tasks BEFORE UPDATE ON tasks
		FOR EACH ROW EXECUTE FUNCTION skip_update_task();
	`, quoteLiteral(taskName)))
	require.NoError(t, err)
}

func quoteLiteral(s string) string { return "'" + s + "'" }

func TestRetryJob_CancelledJob_409_NothingChanged(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "retry-cancelled@example.com", false)
	token := createTestToken(t, e.q, owner.ID)
	job := e.job(t, owner.ID)
	e.task(t, job, "t", "running")

	// Cancel through the real endpoint, so CancelJobTasks squashes the running
	// task onto `failed` exactly as production does. That squash is why retry on
	// a cancelled job would mean "un-cancel everything".
	cancelReq := httptest.NewRequest(http.MethodDelete, "/v1/jobs/"+uuidString(job.ID), nil)
	cancelReq.Header.Set("Authorization", "Bearer "+token)
	cancelRec := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(cancelRec, cancelReq)
	require.Equal(t, http.StatusOK, cancelRec.Code)
	require.Equal(t, "cancelled", e.getJob(t, job.ID).Status)

	tasksBefore, err := e.q.ListTasksByJob(t.Context(), job.ID)
	require.NoError(t, err)

	rec := e.do(t, token, uuidString(job.ID), "task=failed")
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, errBody(t, rec), "job was cancelled")

	assert.Equal(t, "cancelled", e.getJob(t, job.ID).Status,
		"RecomputeJobStatus is cancelled-blind; this endpoint must never reach it from cancelled")
	for _, was := range tasksBefore {
		got := e.getTask(t, was.ID)
		assert.Equal(t, was.Status, got.Status)
		assert.Equal(t, was.AssignmentEpoch, got.AssignmentEpoch)
	}
}

func TestRetryJob_NonTerminalJob_409(t *testing.T) {
	for _, tc := range []struct{ name, taskStatus, jobStatus string }{
		{"pending", "pending", "pending"},
		{"running", "running", "running"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newRetryEnv(t)
			owner := createTestUser(t, e.q, "Owner",
				fmt.Sprintf("retry-nonterm-%s@example.com", tc.name), false)
			token := createTestToken(t, e.q, owner.ID)
			job := e.job(t, owner.ID)
			task := e.task(t, job, "t", tc.taskStatus)
			if tc.taskStatus != "pending" {
				require.Equal(t, tc.jobStatus, e.recompute(t, job))
			}

			rec := e.do(t, token, uuidString(job.ID), "task=all")
			require.Equal(t, http.StatusConflict, rec.Code)
			assert.Contains(t, errBody(t, rec), "job is not finished")

			got := e.getTask(t, task.ID)
			assert.Equal(t, tc.taskStatus, got.Status)
			assert.Equal(t, task.AssignmentEpoch, got.AssignmentEpoch)
		})
	}
}

// Case A. The item leaves the zero-match outcome open; the spec chose 409, so a
// client never gets a success toast and three refetches for a job that did not
// change.
func TestRetryJob_ZeroMatched_409_CaseA(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "retry-zero@example.com", false)
	token := createTestToken(t, e.q, owner.ID)
	job := e.job(t, owner.ID)
	e.task(t, job, "t", "done")
	require.Equal(t, "done", e.recompute(t, job))
	before := e.getJob(t, job.ID)

	rec := e.do(t, token, uuidString(job.ID), "task=failed")
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, errBody(t, rec), "no failed or timed_out tasks")

	after := e.getJob(t, job.ID)
	assert.Equal(t, "done", after.Status)
	assert.Equal(t, before.UpdatedAt.Time, after.UpdatedAt.Time,
		"a refused retry must not stamp updated_at, which the 24h stats buckets window on")
}

// Case B. The selection is non-empty but the guard blocks everything.
func TestRetryJob_DependentsBlocked_409_CaseB_NothingApplied(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "retry-blocked@example.com", false)
	token := createTestToken(t, e.q, owner.ID)
	job := e.job(t, owner.ID)
	tsk := e.task(t, job, "t", "failed")
	dep := e.task(t, job, "d", "done")
	require.NoError(t, e.q.CreateTaskDependency(t.Context(), store.CreateTaskDependencyParams{
		TaskID: dep.ID, DependsOnTaskID: tsk.ID,
	}))
	require.Equal(t, "failed", e.recompute(t, job))

	rec := e.do(t, token, uuidString(job.ID), "task=failed")
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, errBody(t, rec), "dependents that have already run")

	assert.Equal(t, "failed", e.getTask(t, tsk.ID).Status)
	assert.Equal(t, tsk.AssignmentEpoch, e.getTask(t, tsk.ID).AssignmentEpoch)
	assert.Equal(t, "failed", e.getJob(t, job.ID).Status)
}

// Case C, forced deterministically. See installSkipUpdateTrigger: racing two
// retries cannot produce this, because the job row lock serializes them.
func TestRetryJob_PartialMatch_409_CaseC_RollbackIsTotal(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "retry-partial@example.com", false)
	token := createTestToken(t, e.q, owner.ID)
	job := e.job(t, owner.ID)
	keep := e.task(t, job, "t-keep", "failed")
	skip := e.task(t, job, "t-skip", "failed")
	require.Equal(t, "failed", e.recompute(t, job))
	before := e.getJob(t, job.ID)

	installSkipUpdateTrigger(t, e.pool, "t-skip")

	rec := e.do(t, token, uuidString(job.ID), "task=failed")
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, errBody(t, rec), "the job changed while the retry was in flight")

	// Rollback is TOTAL: the row that did update must be back where it started.
	got := e.getTask(t, keep.ID)
	assert.Equal(t, "failed", got.Status, "a partial retry must never be committed")
	assert.Equal(t, keep.AssignmentEpoch, got.AssignmentEpoch,
		"the rolled-back row must not keep its epoch bump")
	assert.Equal(t, "failed", e.getTask(t, skip.ID).Status)
	after := e.getJob(t, job.ID)
	assert.Equal(t, "failed", after.Status)
	assert.Equal(t, before.UpdatedAt.Time, after.UpdatedAt.Time)
}
```

- [ ] **Step 2: Run to verify they pass**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestRetryJob_ -v -timeout 900s`
Expected: PASS.

- [ ] **Step 3: RED proofs**

| Mutation in `handleRetryJob` | Single test reddening | Expected message |
| --- | --- | --- |
| Delete the `case "cancelled":` arm (letting it fall to `default`) | `..._CancelledJob_409_NothingChanged` | `job was cancelled` not in body (the message becomes "job is not finished") |
| Replace the whole `switch job.Status` with nothing | `..._CancelledJob_...` **and** `..._NonTerminalJob_409` | job moves to `running`; **record that two tests redden here - that is the gate doing one job for two cases, not coupling** |
| Delete the `len(selected) == 0` branch | `..._ZeroMatched_409_CaseA` | expected 409, got 409 from case B with the wrong message, or a 200 |
| Change `len(reopened) != len(selected)` to `false` | `..._PartialMatch_409_CaseC_RollbackIsTotal` | expected 409, got 200, and `a partial retry must never be committed` |
| Replace the `defer tx.Rollback(ctx)` with an explicit `tx.Commit` before the case-C return | `..._PartialMatch_...` | `the rolled-back row must not keep its epoch bump` |

- [ ] **Step 4: Commit**

```bash
git add internal/api/jobs_retry_integration_test.go
git commit -m "test(api): the three 409 classes, including a deterministic case C

Cancelled and non-terminal jobs are refused with nothing written; a zero match
is a 409 that does not stamp updated_at; a blocked selection is a distinct 409.
Case C is forced with a BEFORE UPDATE trigger rather than a race, because the
job row lock makes two concurrent retries serialize - the spec's suggested race
cannot produce it. Rollback is proven total, not merely reported."
```

---

## Task 11: The dispatcher wake is gated on the fence having matched

**Files:** Modify `internal/api/jobs_retry_integration_test.go`

- [ ] **Step 1: Write the failing test**

Add imports `"context"`, `"time"`, `"github.com/jackc/pgx/v5"`.

```go
// listenForTaskSubmitted attaches a dedicated connection to
// relay_task_submitted BEFORE the request under test runs, and returns a
// function that reports whether a notification arrived within d.
func listenForTaskSubmitted(t *testing.T, pool *pgxpool.Pool) func(d time.Duration) bool {
	t.Helper()
	conn, err := pool.Acquire(context.Background())
	require.NoError(t, err)
	t.Cleanup(conn.Release)
	_, err = conn.Exec(context.Background(), "LISTEN relay_task_submitted")
	require.NoError(t, err)

	return func(d time.Duration) bool {
		ctx, cancel := context.WithTimeout(context.Background(), d)
		defer cancel()
		_, err := conn.Conn().WaitForNotification(ctx)
		if err == nil {
			return true
		}
		require.ErrorIs(t, err, context.DeadlineExceeded,
			"WaitForNotification failed for a reason other than the timeout")
		return false
	}
}

// Item requirement: "a retry that matched zero tasks must not wake the
// dispatcher and must not report success". NotifyTaskSubmitted is called INSIDE
// the transaction, so Postgres holds the payload until commit - the wake is
// gated on both the row count and the commit.
func TestRetryJob_NotifyTaskSubmitted_FiresOnSuccessOnly(t *testing.T) {
	type setup func(e *retryEnv, t *testing.T, owner pgtype.UUID) (store.Job, string)

	cases := []struct {
		name       string
		query      string
		wantStatus int
		wantNotify bool
		seed       setup
	}{
		{
			name: "success", query: "task=failed", wantStatus: http.StatusOK, wantNotify: true,
			seed: func(e *retryEnv, t *testing.T, owner pgtype.UUID) (store.Job, string) {
				job := e.job(t, owner)
				e.task(t, job, "t", "failed")
				e.recompute(t, job)
				return job, ""
			},
		},
		{
			name: "cancelled_409", query: "task=failed", wantStatus: http.StatusConflict, wantNotify: false,
			seed: func(e *retryEnv, t *testing.T, owner pgtype.UUID) (store.Job, string) {
				job := e.job(t, owner)
				e.task(t, job, "t", "failed")
				e.recompute(t, job)
				_, err := e.q.UpdateJobStatus(t.Context(), store.UpdateJobStatusParams{
					ID: job.ID, Status: "cancelled",
				})
				require.NoError(t, err)
				return job, ""
			},
		},
		{
			name: "zero_matched_409", query: "task=failed", wantStatus: http.StatusConflict, wantNotify: false,
			seed: func(e *retryEnv, t *testing.T, owner pgtype.UUID) (store.Job, string) {
				job := e.job(t, owner)
				e.task(t, job, "t", "done")
				e.recompute(t, job)
				return job, ""
			},
		},
		{
			name: "blocked_409", query: "task=failed", wantStatus: http.StatusConflict, wantNotify: false,
			seed: func(e *retryEnv, t *testing.T, owner pgtype.UUID) (store.Job, string) {
				job := e.job(t, owner)
				tsk := e.task(t, job, "t", "failed")
				dep := e.task(t, job, "d", "done")
				require.NoError(t, e.q.CreateTaskDependency(t.Context(), store.CreateTaskDependencyParams{
					TaskID: dep.ID, DependsOnTaskID: tsk.ID,
				}))
				e.recompute(t, job)
				return job, ""
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newRetryEnv(t)
			owner := createTestUser(t, e.q, "Owner",
				fmt.Sprintf("retry-notify-%s@example.com", tc.name), false)
			token := createTestToken(t, e.q, owner.ID)
			job, _ := tc.seed(e, t, owner.ID)

			wait := listenForTaskSubmitted(t, e.pool)
			rec := e.do(t, token, uuidString(job.ID), tc.query)
			require.Equal(t, tc.wantStatus, rec.Code)

			got := wait(2 * time.Second)
			require.Equal(t, tc.wantNotify, got,
				"dispatcher wake must fire exactly on the success path")
		})
	}
}
```

Note the `cancelled_409` case sets the job status directly through `UpdateJobStatus` rather than the cancel endpoint, because `handleCancelJob` itself does not notify - keeping the LISTEN observation attributable to the retry request alone.

- [ ] **Step 2: Run to verify it passes**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestRetryJob_NotifyTaskSubmitted -v -timeout 900s`
Expected: PASS, 4 subtests.

- [ ] **Step 3: RED proofs**

| Mutation | Single test/subtest reddening | Expected |
| --- | --- | --- |
| Move `q.NotifyTaskSubmitted(ctx)` to immediately after `SelectRetryableTaskIDs`, before the count checks | `zero_matched_409` and `blocked_409` | a notify arrives on a path that wrote nothing |
| Delete the `NotifyTaskSubmitted` call entirely | `success` | no notify within 2s |
| Move `NotifyTaskSubmitted` to after `tx.Commit` | none of these redden | **record this**: the post-commit call is observationally equivalent here, and the in-transaction placement is chosen for the stronger gate (a rollback un-sends it), not for an observable difference. Do not present it as tested behavior it is not. |

- [ ] **Step 4: Commit**

```bash
git add internal/api/jobs_retry_integration_test.go
git commit -m "test(api): the dispatcher wake fires exactly on the retry success path

A LISTEN connection attached before each request: the notify arrives on 200 and
on none of the three 409s. Recorded honestly: moving the call after the commit
does not redden any of these, so the in-transaction placement is chosen for the
stronger gate (a rollback un-sends it), not for an observable difference."
```

---

## Task 12: `retry_count` reset is functional, not cosmetic

**Files:** Modify `internal/api/jobs_retry_integration_test.go`

- [ ] **Step 1: Write the failing test**

```go
// Item criterion: the retry_count decision must be pinned by a test. Asserting
// the column alone would pass against a reset no consumer honors, so this test
// asserts the CONSUMER's predicate - `terminal && task.RetryCount < task.Retries`
// at internal/worker/handler.go:550 - and then proves an agent retry at the new
// generation is genuinely accepted and burns one.
func TestRetryJob_RetryCountResetRestoresAgentRetryBudget(t *testing.T) {
	e := newRetryEnv(t)
	ctx := t.Context()
	owner := createTestUser(t, e.q, "Owner", "retry-budget@example.com", false)
	token := createTestToken(t, e.q, owner.ID)
	job := e.job(t, owner.ID)

	// Seed a task with retries=1 whose single agent retry is already spent, then
	// leave it terminal - all through production statements, no raw SQL.
	task, err := e.q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "t", Commands: []byte(`[["echo","x"]]`),
		Env: []byte("{}"), Requires: []byte("{}"), Retries: 1,
	})
	require.NoError(t, err)
	claimed, err := e.q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: e.w.ID,
	})
	require.NoError(t, err)
	burned, err := e.q.IncrementTaskRetryCount(ctx, store.IncrementTaskRetryCountParams{
		ID: task.ID, AssignmentEpoch: claimed.AssignmentEpoch, WorkerID: claimed.WorkerID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), burned.RetryCount)
	claimed2, err := e.q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: e.w.ID,
	})
	require.NoError(t, err)
	exhausted, err := e.q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
		ID: task.ID, Status: "failed", WorkerID: claimed2.WorkerID,
		AssignmentEpoch: claimed2.AssignmentEpoch,
	})
	require.NoError(t, err)
	require.False(t, exhausted.RetryCount < exhausted.Retries,
		"precondition: the agent-side budget is spent, so handleTaskStatus would NOT retry")
	require.Equal(t, "failed", e.recompute(t, job))

	rec := e.do(t, token, uuidString(job.ID), "task=failed")
	require.Equal(t, http.StatusOK, rec.Code)

	reopened := e.getTask(t, task.ID)
	require.Equal(t, int32(0), reopened.RetryCount, "retry_count must reset to 0")
	require.True(t, reopened.RetryCount < reopened.Retries,
		"the consumer's predicate at internal/worker/handler.go:550 must now be TRUE: "+
			"an operator re-run that starts with zero agent retries dies on the first "+
			"transient error, which is the situation the operator pressed Retry to escape")

	// And it is genuinely spendable: an agent retry at the new generation is
	// accepted and burns one.
	claimed3, err := e.q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: e.w.ID,
	})
	require.NoError(t, err)
	again, err := e.q.IncrementTaskRetryCount(ctx, store.IncrementTaskRetryCountParams{
		ID: task.ID, AssignmentEpoch: claimed3.AssignmentEpoch, WorkerID: claimed3.WorkerID,
	})
	require.NoError(t, err, "the reopened generation must be able to burn a retry")
	require.Equal(t, int32(1), again.RetryCount)
}
```

- [ ] **Step 2: Run to verify it passes**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestRetryJob_RetryCountReset -v -timeout 900s`
Expected: PASS.

- [ ] **Step 3: RED proof**

Mutation: delete `retry_count      = 0,` from `RetryJobTasks`'s `SET` in `internal/store/query/tasks.sql`, then `sqlc generate`.
Run: `go test -tags integration -p 1 ./internal/api/... -run TestRetryJob_RetryCountReset -v -timeout 900s`
Expected: **FAIL** on `retry_count must reset to 0` and, more importantly, on the consumer-predicate assertion. Revert (`git checkout -- internal/store/query/tasks.sql`, `sqlc generate`, Task 2 Step 5/6), re-run.

Note this same mutation also reddens `TestRetryJobTasks_ReopenedRowFields_...` from Task 4. That is not coupling: the two assert different things (the column, and the column's only behavioral consequence), and the item asks for both.

- [ ] **Step 4: Commit**

```bash
git add internal/api/jobs_retry_integration_test.go
git commit -m "test(api): the retry_count reset restores the agent-side retry budget

Asserts the consumer's predicate (task.RetryCount < task.Retries at
internal/worker/handler.go:550) flips from false to true across the operator
retry, and that an agent retry at the new generation is then accepted and burns
one. Asserting the column alone would pass against a reset nothing honors."
```

---

## Task 13: Cancel and retry serialize, and never leave a cancelled job with pending tasks

This is the test that would catch the `GetJobForUpdate` change being dropped as "unrelated scope". The assertion is an invariant that holds under **every** interleaving, so it cannot flake on ordering; the loop shakes out both orders.

**Files:** Modify `internal/api/jobs_retry_integration_test.go`

- [ ] **Step 1: Write the failing test**

Add import `"sync"`.

```go
// A cancel and a retry on the same job, released together. The end state must be
// one of exactly two allowed states, and never (cancelled job, pending tasks) -
// which the dispatcher would happily run, because GetEligibleTasks does not
// consult job status. Ten rounds, because which handler wins the job row lock is
// not controlled here; the assertion holds for both winners.
func TestRetryJob_CancelSerialization_NeverCancelledJobWithPendingTasks(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "retry-serialize@example.com", false)
	token := createTestToken(t, e.q, owner.ID)

	for round := 0; round < 10; round++ {
		job := e.job(t, owner.ID)
		task := e.task(t, job, fmt.Sprintf("t-%d", round), "failed")
		require.Equal(t, "failed", e.recompute(t, job))
		jobID := uuidString(job.ID)

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			e.do(t, token, jobID, "task=failed")
		}()
		go func() {
			defer wg.Done()
			<-start
			req := httptest.NewRequest(http.MethodDelete, "/v1/jobs/"+jobID, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			e.srv.Handler().ServeHTTP(httptest.NewRecorder(), req)
		}()
		close(start)
		wg.Wait()

		gotJob := e.getJob(t, job.ID)
		gotTask := e.getTask(t, task.ID)
		switch gotJob.Status {
		case "cancelled":
			require.NotEqual(t, "pending", gotTask.Status,
				"round %d: a cancelled job must never be left with a pending task - "+
					"GetEligibleTasks does not consult job status, so the farm would run it", round)
		case "running":
			require.Equal(t, "pending", gotTask.Status,
				"round %d: a running job produced by the retry must have its task pending", round)
		case "failed":
			require.Equal(t, "failed", gotTask.Status,
				"round %d: if neither write landed, nothing may have changed", round)
		default:
			t.Fatalf("round %d: unexpected end state job=%s task=%s",
				round, gotJob.Status, gotTask.Status)
		}
	}
}
```

- [ ] **Step 2: Run to verify it passes**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestRetryJob_CancelSerialization -v -timeout 900s`
Expected: PASS.

- [ ] **Step 3: RED proof, with an honest fallback**

Mutation: in `handleCancelJob`, revert the read to `q.GetJob(ctx, id)` (the pre-Task-7 state).
Run the test in a loop: `go test -tags integration -p 1 ./internal/api/... -run TestRetryJob_CancelSerialization -v -timeout 900s -count 3`
Expected: **FAIL** in at least one round on `a cancelled job must never be left with a pending task`.

**If it does not redden after three runs**, the interleave is not being hit by chance. Do **not** delete the test and do **not** drop the `GetJobForUpdate` change. Follow the precedent recorded in `docs/superpowers/specs/2026-08-12-retry-resurrect-status-guard.md` section 7.5: prove the property at the store layer instead, with the exact statement sequence each handler runs (tx A: `GetJob` unlocked -> `ListTasksByJob` -> `CancelJobTasks`; tx B: `GetJobForUpdate` -> `RetryJobTasks` -> `RecomputeJobStatus` -> commit; then commit A), assert the resulting `(job cancelled, task pending)` state, and **record the deviation honestly in the PR** - naming it as a timing-dependent test replaced by a deterministic one, not as a test that passed.

Revert the mutation and re-run before committing.

- [ ] **Step 4: Full api suite and commit**

Run: `go test -tags integration -p 1 ./internal/api/... -timeout 900s` then `go vet -tags integration ./...`
Expected: PASS, clean, with every `TestCancelJob*` test green and untouched.

```bash
git add internal/api/jobs_retry_integration_test.go
git commit -m "test(api): cancel and retry on one job never strand a cancelled job with pending tasks

Both requests released together, ten rounds, asserting an invariant that holds
for either lock winner. Proven RED by reverting handleCancelJob to the unlocked
GetJob read - the state that lets a cancel stamp cancelled over a retry's
freshly pending tasks that GetEligibleTasks would then dispatch."
```

---

## Task 14: README, and the final verification sweep

**Files:** Modify `README.md:1200`

- [ ] **Step 1: Document the route**

In the Jobs table, insert after the `DELETE /v1/jobs/{id}` row:

```markdown
| `POST` | `/v1/jobs/{id}/retry` | Re-run a finished job's tasks. `?task=failed` reopens `failed` **and `timed_out`** tasks; `?task=all` also reopens `done` tasks. `task` is **required** and has no default; absent, empty, repeated or unrecognized values are a 400. Owner or admin (404 on deny). 409 if the job is not `done` or `failed`, if the job was cancelled, if nothing matched the mode, or if a selected task has dependents that already ran. Returns the job plus `tasks_retried` (always >= 1). |
```

- [ ] **Step 2: Run every gate**

```
go test ./... -timeout 120s
go test -tags integration -p 1 ./... -timeout 900s
go vet -tags integration ./...
```
Expected: all PASS, vet clean. If any pre-existing failure appears, measure it **both ways** (with and without this branch) before calling it pre-existing.

- [ ] **Step 3: Confirm the file set matches the spec's Architecture table**

```
git diff --stat main...HEAD
```
Expected exactly these files and no others:
`internal/store/query/tasks.sql`, `internal/store/query/jobs.sql`, `internal/store/tasks.sql.go`, `internal/store/jobs.sql.go`, `internal/store/tasks_status_vocabulary_lockstep_test.go`, `internal/store/incrementtaskretrycount_guard_test.go`, `internal/store/retry_job_tasks_integration_test.go`, `internal/api/jobs.go`, `internal/api/server.go`, `internal/api/jobs_retry_integration_test.go`, `README.md`, plus this plan doc and the spec doc.

**`internal/store/models.go` must NOT appear. No path under `web/` may appear.** If either does, revert it.

- [ ] **Step 4: Re-run the generated-file read-back one final time**

```
git grep -c "It has its own statement, RetryJobTasks" -- internal/store/tasks.sql.go
git grep -c "THE STATUS ALLOW-LIST MUST STAY IN THIS WHERE CLAUSE" -- internal/store/tasks.sql.go
git grep -c "updated_at has TWO writers" -- internal/store/jobs.sql.go
```
Expected `1`, `1`, `1`. Six mutation-and-revert cycles ran through `sqlc generate` in this plan; this is the last chance to catch a discarded regeneration.

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "docs: document POST /v1/jobs/{id}/retry in the REST reference

States that task=failed covers timed_out, that task has no default, and the
four 409 conditions."
```

---

## Phase 6 proposals (do NOT file or apply during implementation)

Per the standing rule, backlog edits are proposed for human accept. Record these in the PR body; the conductor files them.

- Amend `docs/backlog/bug-2026-06-05-jobs-stats-24h-updated-at-proxy.md`'s Context: its assertion that the proxy "would become inaccurate if a `POST /v1/jobs/:id/retry` endpoint is added" did not fire (spec decision 8). The item stays **open**.
- `feature-2026-07-01-job-retry-action` can drop its "backend-blocked" caveat.
- New: `feature-2026-08-13-attempt-scoped-task-logs.md` (medium) - `task_logs` has no attempt or epoch column, so a retried task's log view concatenates runs with no separator.
- New: `feature-2026-08-13-cli-job-retry.md` (low), `feature-2026-08-13-mcp-job-retry-tool.md` (low), `idea-2026-08-13-retry-failed-tasks-of-a-running-job.md` (low), `idea-2026-08-13-recompute-job-status-cancelled-blind.md` (low).
- Close `docs/backlog/feature-2026-08-13-job-retry-endpoint.md` with `/backlog close feature-2026-08-13-job-retry-endpoint`.
