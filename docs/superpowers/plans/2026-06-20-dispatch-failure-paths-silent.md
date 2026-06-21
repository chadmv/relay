# Dispatch Failure Paths: Fail-Fast and Observability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the dispatch loop and `handleTaskStatus` fail poison tasks terminally (instead of silently dropping or infinitely requeueing) and log every DB/decode error path, so persistent data corruption and DB outages are visible and bounded.

**Architecture:** Both bad-JSON paths in `sendTask` (bad `commands`, bad `source`) currently mishandle an already-claimed task: bad `commands` calls `RequeueTask` (causing claim/requeue churn with unbounded `assignment_epoch` growth), and bad `source` returns `false` leaving the task stuck in `dispatched` against a worker that never received it. We replace both with a single `failClaimedTask` helper that marks the claimed task `failed` via the existing epoch-fenced `UpdateTaskStatus` (fenced on the claim's own `assignment_epoch`, terminal so no epoch bump), cascades with `FailDependentTasks`, recomputes job status, and publishes a `task` event - exactly mirroring the proven terminal path in `handleTaskStatus`. Separately, every silently-swallowed error in `dispatch` and `handleTaskStatus` gets a `log.Printf`.

**Tech Stack:** Go, pgx/v5, sqlc, Postgres, testcontainers-go.

**Slice independence:** BACKEND-ONLY. No frontend slice. All work is in `internal/scheduler/dispatch.go` and `internal/worker/handler.go` (plus their integration test files). There is no Phase 3 parallelism to declare. No `.sql`/`.proto` changes, so no `make generate` step is required - every store query this plan uses already exists.

---

## Background: verified current code (real line numbers)

### `internal/scheduler/dispatch.go`

- `dispatch` (lines 68-151) swallows DB errors on `GetEligibleTasks` (69-72), `ListWorkers` (74-77), `ListActiveReservations` (91-94), `CountActiveTasksByAllWorkers` (96-99). The warm-workspace `ListWarmWorkspacesForKeys` error (124-126) is documented best-effort and is intentionally left silent (see Deviation D3).
- `sendTask` (lines 255-327):
  - `ClaimTaskForWorker` (271-277): claims the task (`pending` -> `dispatched`, bumps `assignment_epoch`). On error returns `false` SILENTLY - this includes the benign `pgx.ErrNoRows` race (another dispatcher claimed it). See Deviation D2 for why this one stays mostly silent.
  - **Bad `commands` JSON (280-286):** logs, then `RequeueTask(claimed.ID)` and returns `false`. BUG #2: requeue makes the next cycle re-claim and re-fail forever, bumping `assignment_epoch` unboundedly. `commands` is persistent data, so retry can never succeed.
  - **Bad `source` JSON (299-306):** logs, then `return false` with NO requeue and NO fail. BUG #1: the task stays `dispatched` against `w` which never received it, consuming a slot until that worker disconnects.
  - `registry.Send` failure (313-319): logs + `RequeueTask`. This is CORRECT (transient: worker wedged/gone between claim and send) and is NOT touched by this plan.

Key fact: after `ClaimTaskForWorker` succeeds, `claimed.AssignmentEpoch` is the task's CURRENT epoch and `claimed.WorkerID` is set to `w.ID`. The task is in state `dispatched`.

### `internal/worker/handler.go`

- `handleTaskStatus` (lines 404-499) swallows: `taskID.Scan` (406-408), `GetTask` (410-413), the epoch-gate non-match (417-419, intentional - see Deviation D4), the `default` proto-status case (438-440, intentional), `IncrementTaskRetryCount` (446 - on error it silently does NOT retry), `UpdateTaskStatus` (471-473), `FailDependentTasks` (476). `updateJobStatusFromTasks` -> `RecomputeJobStatus` already swallows internally (612-617).
- The terminal path we are mirroring is lines 463-477: `UpdateTaskStatus` with `AssignmentEpoch: int32(upd.Epoch)` (the gated epoch), then `if terminal { FailDependentTasks }`, then `updateJobStatusFromTasks`, then publish.

### Store queries (all already generated - no `make generate` needed)

`internal/store/query/tasks.sql`:

- `UpdateTaskStatus :one` (lines 12-19): `UPDATE tasks SET status=$2, worker_id=$3, started_at=$4, finished_at=$5 WHERE id=$1 AND assignment_epoch=$6 RETURNING *`. Epoch-FENCED. Does NOT bump the epoch - correct for a terminal write that ends the assignment (no further generation will be dispatched). Returns `pgx.ErrNoRows` if the epoch is stale.
- `FailDependentTasks :exec` (lines 61-74): recursive CTE; marks every transitive dependent whose `status = 'pending'` as `failed`. Only touches `pending` rows, so it is safe to call after the parent is already `failed`.
- `ClaimTaskForWorker :one` (lines 76-86): `pending` -> `dispatched`, bumps `assignment_epoch`, `RETURNING *`.
- `RequeueTask :exec` (lines 88-95): the WRONG tool for poison data (it returns the task to `pending`). We REMOVE its use in the bad-commands path.

`internal/store/query/jobs.sql:89`: `RecomputeJobStatus :one`.

### Generated signatures the plan relies on

- `func (q *Queries) UpdateTaskStatus(ctx, store.UpdateTaskStatusParams) (store.Task, error)` where `UpdateTaskStatusParams{ID, Status, WorkerID, StartedAt, FinishedAt pgtype.Timestamptz, AssignmentEpoch int32}`.
- `func (q *Queries) FailDependentTasks(ctx, failedTaskID pgtype.UUID) error`.
- `store.Task` has fields `ID, JobID, WorkerID pgtype.UUID`, `AssignmentEpoch int32`, `Source []byte`, `Commands []byte`.

### Critical: there is NO pre-existing "mark single task failed with epoch fence" helper

`handleTaskStatus` open-codes the terminal transition with `UpdateTaskStatus` + `FailDependentTasks`. The dispatch path must do the SAME thing. We introduce one private helper, `failClaimedTask`, in `dispatch.go` to DRY the two dispatch call sites; we do NOT add a store query (the existing `UpdateTaskStatus` already provides the fence and we do not want to define a parallel task-failure SQL path - see the Single job-spec / minimal-surface reasoning under Self-Review).

### Test harness facts

- Both packages' loop logic is exercised ONLY by integration tests (build tag `//go:build integration`) because `store.Queries` is a concrete struct (no interface to mock). `internal/scheduler/dispatch_test.go` has `newTestStore(t)`, `newTestStoreWithPool(t)`, the `fakeSender` type, and `uuidStr`. `internal/worker/handler_test.go` has its own harness, `fakeStream`, and `seedWorkerWithAgentToken`.
- New dispatch-failure tests go in `internal/scheduler/dispatch_test.go`. The `handleTaskStatus` logging change is covered by reasoning + the existing handler integration tests continuing to pass (it is log-only; see Task 4).

---

## Epoch-fence handling (the load-bearing detail)

The Invariant: "Every write to `tasks.status` must either fence on `assignment_epoch` or end the assignment. Never call an epoch-fenced query with a zero-value epoch, and never return a task to pending without bumping the epoch."

For the dispatch poison paths the task has just been claimed, so:

- We hold the exact current epoch: `claimed.AssignmentEpoch` (a real, non-zero value returned by `ClaimTaskForWorker`). We pass it to `UpdateTaskStatus` as the fence. This is never a zero-value epoch.
- We transition to `failed`, a TERMINAL status. `UpdateTaskStatus` does not bump the epoch, which is correct: a terminal task is never re-dispatched, so there is no later generation to fence against. (Contrast `RequeueTask`/`IncrementTaskRetryCount`, which return the task toward `pending` and therefore MUST bump - we are not doing that here.)
- The fence still matters for the race window: if between our claim and our fail-write some other path ended the assignment (e.g. a force-cancel via `CancelJobTasks` bumped the epoch), `UpdateTaskStatus` affects zero rows and returns `pgx.ErrNoRows`. We log that and stop - we must NOT retry or requeue, because the poison data guarantees a retry fails and another path now owns the task.

This exactly matches `handleTaskStatus`, which fences on the gated `upd.Epoch` and treats a terminal status as the end of the assignment without bumping.

---

## Task 1: Add the `failClaimedTask` helper (no call sites yet)

Introduce the shared terminal-failure helper used by both poison paths. No behavior change yet (nothing calls it), so the existing dispatch suite must still pass.

**Files:**
- Modify: `internal/scheduler/dispatch.go` (add helper near `sendTask`, after line 327)

- [ ] **Step 1: Add the helper**

Add this function immediately after `sendTask` (after line 327), before `uuidStr`:

```go
// failClaimedTask marks an already-claimed task terminally 'failed' and cascades
// to its dependents. It is the single path the dispatcher uses when a claimed
// task carries poison persistent data (unparseable commands or source JSON):
// retrying can never succeed, so the task must not be requeued (which would churn
// the claim/requeue loop) nor left 'dispatched' (which would leak a worker slot).
//
// Epoch fence: the write goes through UpdateTaskStatus fenced on the claim's own
// assignment_epoch (a real, non-zero value from ClaimTaskForWorker). 'failed' is
// terminal, so the assignment ends and the epoch is intentionally NOT bumped. If
// another path ended the assignment between claim and here, UpdateTaskStatus
// affects zero rows (pgx.ErrNoRows); we log and stop without retry or requeue.
func (d *Dispatcher) failClaimedTask(ctx context.Context, claimed store.Task, reason string) {
	log.Printf("dispatch: failing task %s terminally: %s", uuidStr(claimed.ID), reason)
	updated, err := d.q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
		ID:              claimed.ID,
		Status:          "failed",
		WorkerID:        claimed.WorkerID,
		StartedAt:       claimed.StartedAt,
		FinishedAt:      pgtype.Timestamptz{Time: time.Now(), Valid: true},
		AssignmentEpoch: claimed.AssignmentEpoch,
	})
	if err != nil {
		log.Printf("dispatch: UpdateTaskStatus(failed) for task %s: %v", uuidStr(claimed.ID), err)
		return
	}
	if err := d.q.FailDependentTasks(ctx, claimed.ID); err != nil {
		log.Printf("dispatch: FailDependentTasks for task %s: %v", uuidStr(claimed.ID), err)
	}
	if _, err := d.q.RecomputeJobStatus(ctx, updated.JobID); err != nil {
		log.Printf("dispatch: RecomputeJobStatus for job %s: %v", uuidStr(updated.JobID), err)
	}
	d.broker.Publish(events.Event{
		Type:  "task",
		JobID: uuidStr(updated.JobID),
		Data:  []byte(fmt.Sprintf(`{"id":%q,"status":"failed"}`, uuidStr(updated.ID))),
	})
}
```

- [ ] **Step 2: Build to confirm it compiles (imports `time`, `store`, `events`, `fmt`, `log`, `pgtype` are all already in the file)**

Run: `go build ./internal/scheduler/...`
Expected: no errors. (`dispatch.go` already imports `time`, `log`, `fmt`, `relay/internal/events`, `relay/internal/store`, and `github.com/jackc/pgx/v5/pgtype`.)

- [ ] **Step 3: Run the existing dispatch suite to confirm no regression**

Run: `go test -tags integration -p 1 ./internal/scheduler/... -run TestDispatcher -v -timeout 240s`
Expected: PASS for all existing `TestDispatcher_*` tests (helper is unused so far).

- [ ] **Step 4: Commit**

```bash
git add internal/scheduler/dispatch.go
git commit -m "scheduler: add failClaimedTask helper for terminal poison-task failure"
```

---

## Task 2: Bad `commands` JSON fails terminally instead of requeueing

**Files:**
- Modify: `internal/scheduler/dispatch.go:280-286` (the bad-commands branch in `sendTask`)
- Test: `internal/scheduler/dispatch_test.go`

- [ ] **Step 1: Write the failing integration test**

Add to `internal/scheduler/dispatch_test.go`:

```go
func TestDispatcher_BadCommandsJSON_FailsTaskNoRequeue(t *testing.T) {
	ctx := context.Background()
	q := newTestStore(t)

	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "u", Email: "badcmd@x.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)

	// Poison: commands is valid JSON but not a [][]string (an object, not an array
	// of arrays). json.Unmarshal into [][]string fails - persistent, unretryable.
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "poison", Commands: []byte(`{"bad":"shape"}`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)

	wRow, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name: "w", Hostname: "w", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	w, err := q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID: wRow.ID, Status: "online", LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)

	registry := worker.NewRegistry()
	sender := &fakeSender{}
	registry.Register(uuidStr(w.ID), sender)

	d := scheduler.NewDispatcher(q, registry, events.NewBroker())

	// Run two cycles. The bug requeued, so the second cycle would re-claim and the
	// epoch would climb. The fix marks the task 'failed' on cycle one; cycle two is
	// a no-op because the task is no longer 'pending'.
	d.RunOnce(ctx)
	afterFirst, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", afterFirst.Status, "poison commands must fail the task, not requeue it")
	require.Empty(t, sender.sent, "poison task must never be sent to the worker")

	epochAfterFirst := afterFirst.AssignmentEpoch
	d.RunOnce(ctx)
	afterSecond, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", afterSecond.Status, "task stays failed across cycles")
	require.Equal(t, epochAfterFirst, afterSecond.AssignmentEpoch,
		"no churn: a failed task is not re-claimed, so assignment_epoch must not climb")
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -tags integration -p 1 ./internal/scheduler/... -run TestDispatcher_BadCommandsJSON_FailsTaskNoRequeue -v -timeout 180s`
Expected: FAIL. Current code calls `RequeueTask`, so after cycle one the status is `pending` (not `failed`) and the first assertion fails; even if it reached cycle two, the epoch would have climbed.

- [ ] **Step 3: Replace the bad-commands branch**

In `internal/scheduler/dispatch.go`, replace lines 280-286 (the `if len(claimed.Commands) > 0 { if err := json.Unmarshal(...) { ... RequeueTask ... } }` block) with:

```go
	var commandsArgv [][]string
	if len(claimed.Commands) > 0 {
		if err := json.Unmarshal(claimed.Commands, &commandsArgv); err != nil {
			d.failClaimedTask(ctx, claimed, fmt.Sprintf("bad commands JSON: %v", err))
			return false
		}
	}
```

(The standalone `log.Printf` at old line 282 and the `RequeueTask` call at old line 283 are removed; `failClaimedTask` now owns the logging and the terminal transition.)

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -tags integration -p 1 ./internal/scheduler/... -run TestDispatcher_BadCommandsJSON_FailsTaskNoRequeue -v -timeout 180s`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scheduler/dispatch.go internal/scheduler/dispatch_test.go
git commit -m "scheduler: fail task on bad commands JSON instead of requeue churn"
```

---

## Task 3: Bad `source` JSON fails terminally instead of leaking the slot

**Files:**
- Modify: `internal/scheduler/dispatch.go:299-306` (the bad-source branch in `sendTask`)
- Test: `internal/scheduler/dispatch_test.go`

- [ ] **Step 1: Write the failing integration test**

Add to `internal/scheduler/dispatch_test.go`:

```go
func TestDispatcher_BadSourceJSON_FailsTaskNoLeak(t *testing.T) {
	ctx := context.Background()
	q := newTestStore(t)

	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "u", Email: "badsrc@x.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)

	// Poison: source is a non-empty blob that is NOT valid JSON for SourceSpec.
	// taskIsSourceBearing returns false for an unparseable spec, so selectWorker
	// does NOT require a workspace provider - the task is selected, claimed, then
	// the in-sendTask json.Unmarshal of claimed.Source fails. We give the worker
	// SupportsWorkspaces=true so selection is unambiguous regardless.
	wRow, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name: "w", Hostname: "w", CpuCores: 1, RamGb: 1, Os: "linux",
		SupportsWorkspaces: true,
	})
	require.NoError(t, err)
	w, err := q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID: wRow.ID, Status: "online", LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)

	task, err := q.CreateTaskWithSource(ctx, store.CreateTaskWithSourceParams{
		JobID: job.ID, Name: "poison-src", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`), Source: []byte(`not-json`),
	})
	require.NoError(t, err)

	registry := worker.NewRegistry()
	sender := &fakeSender{}
	registry.Register(uuidStr(w.ID), sender)

	d := scheduler.NewDispatcher(q, registry, events.NewBroker())
	d.RunOnce(ctx)

	got, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", got.Status,
		"poison source must fail the task, not leave it dispatched (slot leak)")
	require.Empty(t, sender.sent, "poison task must never be sent to the worker")
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -tags integration -p 1 ./internal/scheduler/... -run TestDispatcher_BadSourceJSON_FailsTaskNoLeak -v -timeout 180s`
Expected: FAIL. Current code `return false` after the claim, leaving the task in `dispatched`, so the `"failed"` assertion fails.

- [ ] **Step 3: Replace the bad-source branch**

In `internal/scheduler/dispatch.go`, replace lines 299-306 (the `if len(claimed.Source) > 0 { var ss ...; if err := json.Unmarshal(...) { log.Printf(...); return false } ... }` block) with:

```go
	if len(claimed.Source) > 0 {
		var ss api.SourceSpec
		if err := json.Unmarshal(claimed.Source, &ss); err != nil {
			d.failClaimedTask(ctx, claimed, fmt.Sprintf("bad source JSON: %v", err))
			return false
		}
		dt.Source = sourceSpecToProto(&ss)
	}
```

(The bare `log.Printf` at old line 302 is removed; `failClaimedTask` owns it. The `return false` now follows a terminal failure rather than a silent leak.)

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -tags integration -p 1 ./internal/scheduler/... -run TestDispatcher_BadSourceJSON_FailsTaskNoLeak -v -timeout 180s`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scheduler/dispatch.go internal/scheduler/dispatch_test.go
git commit -m "scheduler: fail task on bad source JSON instead of leaking worker slot"
```

---

## Task 4: Log every swallowed DB error in `dispatch`

Log-only changes. There is no behavior change to assert in a test (the early-return on a DB error is preserved by design - a transient DB hiccup should skip the cycle, not crash), so this task is verified by code review + the existing dispatch suite continuing to pass. The value is observability: a persistent DB outage that stops dispatch now leaves a trail.

**Files:**
- Modify: `internal/scheduler/dispatch.go` - the four swallowed error sites in `dispatch` (lines 69-72, 74-77, 91-94, 96-99)

- [ ] **Step 1: Add logging to `GetEligibleTasks`**

Replace lines 69-72:

```go
	tasks, err := d.q.GetEligibleTasks(ctx)
	if err != nil {
		log.Printf("dispatch: GetEligibleTasks: %v", err)
		return
	}
	if len(tasks) == 0 {
		return
	}
```

(The empty-result case stays a silent return - it is the normal idle path, not an error.)

- [ ] **Step 2: Add logging to `ListWorkers`**

Replace lines 74-77:

```go
	workers, err := d.q.ListWorkers(ctx)
	if err != nil {
		log.Printf("dispatch: ListWorkers: %v", err)
		return
	}
```

- [ ] **Step 3: Add logging to `ListActiveReservations`**

Replace lines 91-94:

```go
	reservations, err := d.q.ListActiveReservations(ctx)
	if err != nil {
		log.Printf("dispatch: ListActiveReservations: %v", err)
		return
	}
```

- [ ] **Step 4: Add logging to `CountActiveTasksByAllWorkers`**

Replace lines 96-99:

```go
	counts, err := d.q.CountActiveTasksByAllWorkers(ctx)
	if err != nil {
		log.Printf("dispatch: CountActiveTasksByAllWorkers: %v", err)
		return
	}
```

(Leave the `ListWarmWorkspacesForKeys` error at lines 124-126 silent: it is documented best-effort warm-scoring and a failure correctly falls through to cold dispatch. See Deviation D3.)

- [ ] **Step 5: Build and run the dispatch suite for regression**

Run: `go build ./internal/scheduler/...`
Expected: no errors.

Run: `go test -tags integration -p 1 ./internal/scheduler/... -run TestDispatcher -v -timeout 300s`
Expected: PASS for all `TestDispatcher_*` tests (including the two added in Tasks 2-3).

- [ ] **Step 6: Commit**

```bash
git add internal/scheduler/dispatch.go
git commit -m "scheduler: log swallowed DB errors in dispatch loop"
```

---

## Task 5: Log every swallowed error in `handleTaskStatus`

Log-only changes for observability of a lost task-status update (e.g. a dropped `done`). Verified by code review + the existing handler integration suite continuing to pass.

**Files:**
- Modify: `internal/worker/handler.go` - the swallowed sites in `handleTaskStatus` (lines 405-477)

- [ ] **Step 1: Log the unparseable task-id**

Replace lines 405-408:

```go
	var taskID pgtype.UUID
	if err := taskID.Scan(upd.TaskId); err != nil {
		log.Printf("worker: handleTaskStatus bad task id %q: %v", upd.TaskId, err)
		return
	}
```

- [ ] **Step 2: Log the `GetTask` failure**

Replace lines 410-413:

```go
	task, err := h.q.GetTask(ctx, taskID)
	if err != nil {
		log.Printf("worker: handleTaskStatus GetTask %s: %v", upd.TaskId, err)
		return
	}
```

- [ ] **Step 3: Log a failed retry-count increment**

Replace the retry block at lines 445-451:

```go
	// Retry if applicable. Epoch guard above ensures we don't double-retry.
	if terminal && task.RetryCount < task.Retries {
		if _, err := h.q.IncrementTaskRetryCount(ctx, taskID); err != nil {
			log.Printf("worker: handleTaskStatus IncrementTaskRetryCount %s: %v", upd.TaskId, err)
		} else {
			updateJobStatusFromTasks(ctx, h.q, task.JobID)
			_ = h.q.NotifyTaskSubmitted(ctx)
		}
		return
	}
```

- [ ] **Step 4: Log the `UpdateTaskStatus` failure**

Replace lines 463-473 (keep the params identical; only add the log on the error path):

```go
	updated, err := h.q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
		ID:              taskID,
		Status:          statusStr,
		WorkerID:        task.WorkerID,
		StartedAt:       startedAt,
		FinishedAt:      finishedAt,
		AssignmentEpoch: int32(upd.Epoch),
	})
	if err != nil {
		log.Printf("worker: handleTaskStatus UpdateTaskStatus %s -> %s: %v", upd.TaskId, statusStr, err)
		return
	}
```

- [ ] **Step 5: Log a failed dependent-cascade**

Replace lines 475-477:

```go
	if terminal {
		if err := h.q.FailDependentTasks(ctx, taskID); err != nil {
			log.Printf("worker: handleTaskStatus FailDependentTasks %s: %v", upd.TaskId, err)
		}
	}
```

(Leave the epoch-gate non-match at lines 417-419 and the proto `default` case at 438-440 silent - those are not errors, they are normal stale-update and unknown-status rejections. See Deviation D4. `updateJobStatusFromTasks` already logs nothing internally on a `RecomputeJobStatus` error; that pre-existing swallow is out of scope - see Deviation D5.)

- [ ] **Step 6: Build and run the handler integration suite for regression**

Run: `go build ./internal/worker/...`
Expected: no errors.

Run: `go test -tags integration -p 1 ./internal/worker/... -run TestHandler -v -timeout 300s`
Expected: PASS for all existing handler tests (these are log-only changes; status transitions are unchanged).

- [ ] **Step 7: Commit**

```bash
git add internal/worker/handler.go
git commit -m "worker: log swallowed errors in handleTaskStatus"
```

---

## Task 6: Final verification

- [ ] **Step 1: Unit tests + vet (no Docker)**

Run: `make test`
Expected: PASS (integration tests are gated out; nothing in this change has non-integration unit coverage, so this confirms no compile/vet breakage).

Run: `go vet ./internal/scheduler/... ./internal/worker/...`
Expected: no findings.

- [ ] **Step 2: Full scheduler + worker integration suites, clean run**

Run: `go test -tags integration -p 1 ./internal/scheduler/... -v -timeout 360s`
Expected: all PASS, including `TestDispatcher_BadCommandsJSON_FailsTaskNoRequeue` and `TestDispatcher_BadSourceJSON_FailsTaskNoLeak`.

Run: `go test -tags integration -p 1 ./internal/worker/... -v -timeout 360s`
Expected: all PASS.

- [ ] **Step 3: Confirm only the four intended files changed**

Run: `git status --short`
Expected: across all commits only `internal/scheduler/dispatch.go`, `internal/scheduler/dispatch_test.go`, and `internal/worker/handler.go` were touched. No generated-file churn (no `.sql`/`.proto` edits were made).

- [ ] **Step 4: Close the backlog item**

The backlog item `docs/backlog/bug-2026-06-10-dispatch-failure-paths-silent.md` is resolved by this work. Close it with the command (NOT by hand-editing the status field):

```
/backlog close dispatch-failure-paths-silent
```

Expected: the file is `git mv`-ed to `docs/backlog/closed/`, stamped `status: closed` + `closed:`/`resolution:` frontmatter, a `## Resolution` note appended, and committed.

---

## Deviations from the backlog proposal

- **D1 (refinement, not deviation): bad-`commands` and bad-`source` share one helper.** The proposal says "mark the task `failed` (and run `FailDependentTasks`) for both bad-JSON cases." Both cases occur AFTER `ClaimTaskForWorker`, so the task is already `dispatched` at epoch `claimed.AssignmentEpoch`. Marking it failed therefore goes through the existing epoch-fenced `UpdateTaskStatus` (fenced on that claim epoch, terminal, no epoch bump) + `FailDependentTasks` - the identical sequence `handleTaskStatus` already uses. I add one private helper `failClaimedTask` rather than a new store query, to avoid a parallel task-failure SQL path and stay within the existing `UpdateTaskStatus` fence. I also recompute job status and publish a `task` event in the helper, matching `handleTaskStatus`, so a job is not stranded `running` after its only task fails on poison data.

- **D2: `ClaimTaskForWorker` error stays mostly silent (recommended).** The proposal says log "every error path." `ClaimTaskForWorker` returning `pgx.ErrNoRows` is the documented benign claim race (another dispatcher won) and fires on the normal happy path; logging it would be noisy and misleading. RECOMMENDATION: leave the `ErrNoRows` case silent but log any OTHER error. Concretely, replace lines 275-277 with:
  ```go
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			log.Printf("dispatch: ClaimTaskForWorker for task %s: %v", uuidStr(task.ID), err)
		}
		return false
	}
  ```
  This requires adding `"errors"` and `"github.com/jackc/pgx/v5"` to the `dispatch.go` imports. Fold this into Task 4 as an additional step if the reviewer agrees; it is called out separately because it is the one error site where blanket logging would be wrong. (If kept, add `errors` + `pgx` imports and a one-line note to Task 4 Step 1.)

- **D3: `ListWarmWorkspacesForKeys` error stays silent (recommended).** It is documented best-effort warm-scoring (dispatch.go:124-126); a failure correctly degrades to cold dispatch and is not a correctness gap. Logging it on every transient hiccup would be noise. Recommend leaving it silent (out of scope for "failure paths are silent," which is about correctness/observability gaps, not best-effort optimizations).

- **D4: epoch-gate non-match and unknown-proto-status stay silent (recommended).** In `handleTaskStatus`, lines 417-419 (stale epoch) and 438-440 (unknown status enum) are not errors - they are expected rejections of zombie/unknown messages. Logging them would generate noise on every legitimate reassignment. Recommend leaving them silent.

- **D5: `RecomputeJobStatus` swallow inside `updateJobStatusFromTasks` is out of scope.** `updateJobStatusFromTasks` (handler.go:612-617) already swallows a `RecomputeJobStatus` error and returns `""`. That is a pre-existing, shared helper used well beyond these two paths; adding logging there is a broader change than this bug. Recommend NOT touching it (surgical-changes rule). The dispatch path's own `RecomputeJobStatus` call (in `failClaimedTask`, Task 1) DOES log, because that call is new code we own.

---

## Self-Review

- **Spec coverage:**
  - Bug #1 (bad source leaves task `dispatched`, leaks slot): Task 3, asserted by `TestDispatcher_BadSourceJSON_FailsTaskNoLeak` (status must be `failed`).
  - Bug #2 (bad commands requeues -> infinite churn, unbounded epoch): Task 2, asserted by `TestDispatcher_BadCommandsJSON_FailsTaskNoRequeue` (status `failed` after cycle one; `assignment_epoch` unchanged across a second cycle).
  - Bug #3 (silent DB errors): Task 4 (dispatch) + Task 5 (handleTaskStatus) add `log.Printf` to every genuine error path; Deviations D2-D5 justify the handful of intentionally-silent sites.
  - Proposal "mark failed AND run FailDependentTasks for both bad-JSON cases": delivered by `failClaimedTask` (Task 1), wired in Tasks 2-3.
- **Placeholder scan:** every code step shows complete real code; no TODO/TBD.
- **Type consistency:** `failClaimedTask(ctx, claimed store.Task, reason string)` is defined in Task 1 and called with that exact signature in Task 2 (`fmt.Sprintf("bad commands JSON: %v", err)`) and Task 3 (`fmt.Sprintf("bad source JSON: %v", err)`). `UpdateTaskStatusParams` fields used in `failClaimedTask` match the generated struct and `handleTaskStatus`'s usage. `RecomputeJobStatus(ctx, jobID) (string, error)` is used per its generated signature.
- **Ordering / invariants:**
  - Epoch fence: every `tasks.status` write in this plan goes through the already-fenced `UpdateTaskStatus`; the dispatch path passes the real non-zero `claimed.AssignmentEpoch`, and `failed` is terminal so no epoch bump is required (and none is done). No query is called with a zero-value epoch, and no task is returned to `pending` (we removed the wrong `RequeueTask` in Task 2). Documented in the "Epoch-fence handling" section.
  - Single JSON entry point / job-spec pipeline: unaffected - no request-body reads, no spec parsing added.
  - No `.sql`/`.proto` edits, so no `make generate` and no generated-file churn (confirmed in Task 6 Step 3).
  - Surgical scope: only the two poison branches and the swallowed-error sites are changed; the correct `registry.Send`-failure requeue path (dispatch.go:313-319) is left untouched.
